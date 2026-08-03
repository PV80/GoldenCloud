using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Runtime.Versioning;
using System.Threading;
using System.Threading.Tasks;
using GoldenCloud.Core.Credentials;
using GoldenCloud.Core.Mounting;
using GoldenCloud.Core.Reconnect;

namespace GoldenCloud.Tray;

internal enum MountState
{
    SignedOut,
    Connecting,
    Connected,
    Retrying,
    Failed,
}

/// <summary>What the tray icon and menu should be showing right now.</summary>
internal sealed class MountStatus
{
    public MountStatus(MountState state, string message, MountStrategy? strategy, string driveLetter)
    {
        State = state;
        Message = message;
        Strategy = strategy;
        DriveLetter = driveLetter;
    }

    public MountState State { get; }

    public string Message { get; }

    public MountStrategy? Strategy { get; }

    public string DriveLetter { get; }
}

/// <summary>
/// Keeps the drive mounted. Mounts on demand, watches it, and re-mounts with
/// exponential backoff when it drops (D-005).
///
/// All of the decision logic it relies on — command generation, the backoff
/// schedule — lives in GoldenCloud.Core and is unit-tested; this class is the
/// thin Windows-specific shell that runs processes and touches the filesystem.
/// </summary>
[SupportedOSPlatform("windows")]
internal sealed class MountSupervisor : IDisposable
{
    private static readonly TimeSpan HealthInterval = TimeSpan.FromSeconds(15);
    private static readonly TimeSpan HealthProbeTimeout = TimeSpan.FromSeconds(10);
    private static readonly TimeSpan MountAppearTimeout = TimeSpan.FromSeconds(30);

    private readonly string _serverUrl;
    private readonly AppSettings _settings;
    private readonly Func<StoredCredential?> _credentialProvider;
    private readonly IPasswordObscurer _obscurer;
    private readonly RcloneMountCommandBuilder _rcloneBuilder = new();
    private readonly NetUseMountCommandBuilder _netUseBuilder = new();
    private readonly ReconnectBackoff _backoff = new();
    private readonly object _gate = new();

    private CancellationTokenSource? _cancellation;
    private Task? _loop;
    private Process? _rcloneProcess;
    private MountStrategy? _activeStrategy;
    private bool _disposed;

    public MountSupervisor(
        string serverUrl,
        AppSettings settings,
        Func<StoredCredential?> credentialProvider,
        IPasswordObscurer obscurer)
    {
        _serverUrl = serverUrl;
        _settings = settings;
        _credentialProvider = credentialProvider;
        _obscurer = obscurer;
    }

    /// <summary>Raised on a background thread; the tray marshals it to the UI.</summary>
    public event Action<MountStatus>? StatusChanged;

    public MountStatus Current { get; private set; } =
        new(MountState.SignedOut, "Not signed in.", null, DriveLetters.Default);

    public void Start()
    {
        lock (_gate)
        {
            if (_loop is not null)
            {
                return;
            }

            _cancellation = new CancellationTokenSource();
            CancellationToken token = _cancellation.Token;
            _loop = Task.Run(() => SuperviseAsync(token), token);
        }
    }

    /// <summary>Forces an immediate re-mount attempt; used by the Reconnect menu item.</summary>
    public void RequestReconnect()
    {
        _backoff.Reset();
        TearDownMount();
    }

    /// <summary>Unmounts and stops watching; used by Sign out and Quit.</summary>
    public void Stop()
    {
        CancellationTokenSource? cancellation;
        Task? loop;

        lock (_gate)
        {
            cancellation = _cancellation;
            loop = _loop;
            _cancellation = null;
            _loop = null;
        }

        try
        {
            cancellation?.Cancel();
            loop?.Wait(TimeSpan.FromSeconds(5));
        }
        catch (AggregateException)
        {
            // The loop only ever ends by cancellation.
        }
        finally
        {
            cancellation?.Dispose();
        }

        TearDownMount();
        Report(MountState.SignedOut, "Not signed in.");
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        Stop();
    }

    // ---- the loop -----------------------------------------------------------

    private async Task SuperviseAsync(CancellationToken token)
    {
        while (!token.IsCancellationRequested)
        {
            try
            {
                StoredCredential? credential = _credentialProvider();
                if (credential is null)
                {
                    Report(MountState.SignedOut, "Not signed in.");
                    await Task.Delay(HealthInterval, token).ConfigureAwait(false);
                    continue;
                }

                if (await IsDriveHealthyAsync(token).ConfigureAwait(false))
                {
                    _backoff.Reset();
                    Report(MountState.Connected, _settings.DriveLetter + " is connected.");
                    await Task.Delay(HealthInterval, token).ConfigureAwait(false);
                    continue;
                }

                Report(MountState.Connecting, "Connecting " + _settings.DriveLetter + "...");

                bool mounted = await TryMountAsync(credential, token).ConfigureAwait(false);
                if (mounted)
                {
                    _backoff.Reset();
                    Report(MountState.Connected, _settings.DriveLetter + " is connected.");
                    await Task.Delay(HealthInterval, token).ConfigureAwait(false);
                    continue;
                }

                TimeSpan delay = _backoff.NextDelay();
                Report(
                    MountState.Retrying,
                    "Not connected. Retrying in " + Describe(delay) + " (attempt " + _backoff.Attempt + ").");
                await Task.Delay(delay, token).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            catch (Exception ex)
            {
                Log("supervisor error: " + ex.Message);
                Report(MountState.Failed, "Something went wrong: " + ex.Message);

                try
                {
                    await Task.Delay(_backoff.NextDelay(), token).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    return;
                }
            }
        }
    }

    private static string Describe(TimeSpan delay) =>
        delay.TotalSeconds < 90
            ? Math.Round(delay.TotalSeconds) + "s"
            : Math.Round(delay.TotalMinutes, 1) + " min";

    // ---- health -------------------------------------------------------------

    /// <summary>
    /// Existence alone is not enough: a dropped WebDAV mapping can leave the
    /// drive letter present but unresponsive, so this also reads the root. The
    /// probe runs on a pool thread with a timeout because a wedged redirector
    /// can block a directory listing indefinitely.
    /// </summary>
    private async Task<bool> IsDriveHealthyAsync(CancellationToken token)
    {
        string root;
        try
        {
            root = DriveLetters.ToRootPath(_settings.DriveLetter);
        }
        catch (ArgumentException)
        {
            return false;
        }

        Task<bool> probe = Task.Run(
            () =>
            {
                try
                {
                    if (!Directory.Exists(root))
                    {
                        return false;
                    }

                    using IEnumerator<string> entries = Directory.EnumerateFileSystemEntries(root).GetEnumerator();
                    entries.MoveNext();
                    return true;
                }
                catch (Exception)
                {
                    return false;
                }
            },
            CancellationToken.None);

        Task completed = await Task
            .WhenAny(probe, Task.Delay(HealthProbeTimeout, token))
            .ConfigureAwait(false);

        if (!ReferenceEquals(completed, probe))
        {
            token.ThrowIfCancellationRequested();
            return false;
        }

        return await probe.ConfigureAwait(false);
    }

    // ---- mounting -----------------------------------------------------------

    private MountStrategy ChooseStrategy()
    {
        return _settings.Strategy switch
        {
            StrategyPreference.ForceRclone => MountStrategy.Rclone,
            StrategyPreference.ForceNetUse => MountStrategy.NetUse,
            _ => WinFspDetector.IsInstalled() && File.Exists(WindowsPaths.RcloneExe)
                ? MountStrategy.Rclone
                : MountStrategy.NetUse,
        };
    }

    private async Task<bool> TryMountAsync(StoredCredential credential, CancellationToken token)
    {
        TearDownMount();

        MountStrategy strategy = ChooseStrategy();
        _activeStrategy = strategy;

        MountRequest request;
        try
        {
            request = new MountRequest(_serverUrl, credential.Username, _settings.DriveLetter)
            {
                RclonePath = WindowsPaths.RcloneExe,
                NetPath = WindowsPaths.NetExe,
                CacheDirectory = WindowsPaths.CacheDirectory,
                LogFile = WindowsPaths.LogFile,
                Persistent = false,
            };
        }
        catch (ArgumentException ex)
        {
            Log("cannot build mount request: " + ex.Message);
            Report(MountState.Failed, ex.Message);
            return false;
        }

        return strategy == MountStrategy.Rclone
            ? await MountWithRcloneAsync(request, credential, token).ConfigureAwait(false)
            : await MountWithNetUseAsync(request, credential, token).ConfigureAwait(false);
    }

    private async Task<bool> MountWithRcloneAsync(
        MountRequest request,
        StoredCredential credential,
        CancellationToken token)
    {
        MountCommand command;
        try
        {
            command = _rcloneBuilder.BuildMount(request, _obscurer.Obscure(credential.Password));
        }
        catch (Exception ex)
        {
            Log("rclone command build failed: " + ex.Message);
            return false;
        }

        var startInfo = new ProcessStartInfo(command.FileName)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
        };

        foreach (string argument in command.Arguments)
        {
            startInfo.ArgumentList.Add(argument);
        }

        // The obscured password goes here and only here. A process command line
        // is readable by other users on Windows; its environment block is not.
        foreach (KeyValuePair<string, string> variable in command.Environment)
        {
            startInfo.Environment[variable.Key] = variable.Value;
        }

        Log("mount (rclone): " + command.RedactedCommandLine);

        try
        {
            Process? process = Process.Start(startInfo);
            if (process is null)
            {
                return false;
            }

            _rcloneProcess = process;
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            Log("rclone failed to start: " + ex.Message);
            return false;
        }

        return await WaitForDriveAsync(token).ConfigureAwait(false);
    }

    private async Task<bool> MountWithNetUseAsync(
        MountRequest request,
        StoredCredential credential,
        CancellationToken token)
    {
        MountCommand command = _netUseBuilder.BuildMount(request, credential.Password);

        var startInfo = new ProcessStartInfo(command.FileName)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };

        foreach (string argument in command.Arguments)
        {
            startInfo.ArgumentList.Add(argument);
        }

        Log("mount (net use): " + command.RedactedCommandLine);

        try
        {
            using Process? process = Process.Start(startInfo);
            if (process is null)
            {
                return false;
            }

            // net.exe was given "*" for the password, so it reads it from here.
            if (command.StandardInput is not null)
            {
                await process.StandardInput.WriteAsync(command.StandardInput).ConfigureAwait(false);
                await process.StandardInput.FlushAsync().ConfigureAwait(false);
            }

            process.StandardInput.Close();

            Task<string> standardOutput = process.StandardOutput.ReadToEndAsync();
            Task<string> standardError = process.StandardError.ReadToEndAsync();

            await process.WaitForExitAsync(token).ConfigureAwait(false);

            string output = (await standardOutput.ConfigureAwait(false)) +
                            (await standardError.ConfigureAwait(false));

            if (process.ExitCode != 0)
            {
                Log("net use failed (" + process.ExitCode + "): " + output.Trim());
                return false;
            }
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception
                                      or InvalidOperationException
                                      or IOException)
        {
            Log("net use failed to start: " + ex.Message);
            return false;
        }

        return await WaitForDriveAsync(token).ConfigureAwait(false);
    }

    /// <summary>Polls until the drive letter answers, or the timeout expires.</summary>
    private async Task<bool> WaitForDriveAsync(CancellationToken token)
    {
        DateTime deadline = DateTime.UtcNow + MountAppearTimeout;

        while (DateTime.UtcNow < deadline)
        {
            if (await IsDriveHealthyAsync(token).ConfigureAwait(false))
            {
                return true;
            }

            await Task.Delay(TimeSpan.FromMilliseconds(500), token).ConfigureAwait(false);
        }

        return false;
    }

    /// <summary>
    /// Ends whichever mount is in place. rclone mounts end with the process;
    /// redirector mappings need an explicit "net use /delete".
    /// </summary>
    private void TearDownMount()
    {
        Process? process = _rcloneProcess;
        _rcloneProcess = null;

        if (process is not null)
        {
            try
            {
                if (!process.HasExited)
                {
                    process.Kill(entireProcessTree: true);
                    process.WaitForExit(10_000);
                }
            }
            catch (Exception ex) when (ex is InvalidOperationException
                                          or System.ComponentModel.Win32Exception
                                          or NotSupportedException)
            {
                // Already gone.
            }
            finally
            {
                process.Dispose();
            }
        }

        // Always clear a stale redirector mapping too: a half-dead mapping on the
        // same letter stops either strategy from re-mounting.
        try
        {
            MountRequest request = BuildRequestForUnmount();
            MountCommand? unmount = _netUseBuilder.BuildUnmount(request);
            if (unmount is not null)
            {
                RunQuietly(unmount);
            }
        }
        catch (ArgumentException)
        {
            // No usable drive letter; nothing to clear.
        }

        _activeStrategy = null;
    }

    private MountRequest BuildRequestForUnmount()
    {
        // Username is irrelevant for "net use /delete", but MountRequest insists
        // on a non-empty one, so use a placeholder rather than weaken the type.
        string username = _credentialProvider()?.Username ?? "goldencloud";
        return new MountRequest(_serverUrl, string.IsNullOrWhiteSpace(username) ? "goldencloud" : username, _settings.DriveLetter)
        {
            NetPath = WindowsPaths.NetExe,
        };
    }

    private static void RunQuietly(MountCommand command)
    {
        try
        {
            var startInfo = new ProcessStartInfo(command.FileName)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
            };

            foreach (string argument in command.Arguments)
            {
                startInfo.ArgumentList.Add(argument);
            }

            using Process? process = Process.Start(startInfo);
            process?.WaitForExit(15_000);
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            // Best effort.
        }
    }

    // ---- reporting ----------------------------------------------------------

    private void Report(MountState state, string message)
    {
        var status = new MountStatus(state, message, _activeStrategy, _settings.DriveLetter);
        Current = status;

        try
        {
            StatusChanged?.Invoke(status);
        }
        catch (Exception ex)
        {
            Log("status handler threw: " + ex.Message);
        }
    }

    /// <summary>
    /// Appends to %LOCALAPPDATA%\GoldenCloud\mount.log. Only ever handed strings
    /// built from <see cref="MountCommand.RedactedCommandLine"/> and exception
    /// messages, so no secret can reach it.
    /// </summary>
    internal static void Log(string message)
    {
        try
        {
            string line = DateTime.UtcNow.ToString("yyyy-MM-dd HH:mm:ss'Z'") + "  " + message + System.Environment.NewLine;
            File.AppendAllText(Path.Combine(WindowsPaths.DataDirectory, "client.log"), line);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Logging must never take the app down.
        }
    }
}
