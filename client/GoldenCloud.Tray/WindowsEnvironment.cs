using System;
using System.Diagnostics;
using System.IO;
using System.Runtime.Versioning;
using Microsoft.Win32;

namespace GoldenCloud.Tray;

/// <summary>Is WinFsp present? Decides which mount strategy is used (D-005).</summary>
[SupportedOSPlatform("windows")]
internal static class WinFspDetector
{
    /// <summary>WinFsp registers itself under WOW6432Node even on 64-bit Windows.</summary>
    private const string WinFspKey32 = @"SOFTWARE\WOW6432Node\WinFsp";
    private const string WinFspKey64 = @"SOFTWARE\WinFsp";
    private const string WinFspServiceKey = @"SYSTEM\CurrentControlSet\Services\WinFsp";

    public static bool IsInstalled() => IsInstalled(out _);

    public static bool IsInstalled(out string? installDirectory)
    {
        installDirectory = null;

        foreach (string keyPath in new[] { WinFspKey32, WinFspKey64 })
        {
            try
            {
                using RegistryKey? key = Registry.LocalMachine.OpenSubKey(keyPath);
                if (key is not null)
                {
                    installDirectory = key.GetValue("InstallDir") as string;
                    return true;
                }
            }
            catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException)
            {
                // Locked-down machine; fall through to the other probes.
            }
        }

        try
        {
            using RegistryKey? service = Registry.LocalMachine.OpenSubKey(WinFspServiceKey);
            if (service is not null)
            {
                return true;
            }
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException)
        {
            // Ignore.
        }

        try
        {
            string driver = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.System),
                "drivers",
                "winfsp-x64.sys");
            if (File.Exists(driver))
            {
                return true;
            }
        }
        catch (IOException)
        {
            // Ignore.
        }

        return false;
    }
}

/// <summary>
/// The Windows WebDAV redirector caps downloads at 50 MB by default. Raising it
/// is a machine-wide HKLM change that needs administrator rights and a restart
/// of the WebClient service, so it is only ever done after explicit consent
/// (D-005).
/// </summary>
[SupportedOSPlatform("windows")]
internal static class WebClientLimit
{
    private const string ParametersKey = @"SYSTEM\CurrentControlSet\Services\WebClient\Parameters";
    private const string ValueName = "FileSizeLimitInBytes";

    /// <summary>What Windows uses when the value is absent: 50,000,000 bytes.</summary>
    public const long WindowsDefaultBytes = 50_000_000L;

    /// <summary>0xFFFFFFFF, the largest value the DWORD accepts.</summary>
    public const long DesiredBytes = 4_294_967_295L;

    /// <summary>The effective limit, or null when it could not be read.</summary>
    public static long? ReadLimitBytes()
    {
        try
        {
            using RegistryKey? key = Registry.LocalMachine.OpenSubKey(ParametersKey);
            if (key is null)
            {
                return null;
            }

            object? value = key.GetValue(ValueName);
            return value switch
            {
                int i => (long)(uint)i,
                long l => l,
                _ => null,
            };
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    /// <summary>True when large files would fail to copy through the fallback mount.</summary>
    public static bool IsRestrictive()
    {
        long limit = ReadLimitBytes() ?? WindowsDefaultBytes;
        return limit <= WindowsDefaultBytes;
    }

    /// <summary>
    /// Writes the new limit and restarts the WebClient service. Requires an
    /// elevated process; <see cref="Program"/> re-launches itself with
    /// <c>--fix-webclient-limit</c> to get there.
    /// </summary>
    public static bool ApplyFromElevatedProcess()
    {
        try
        {
            using RegistryKey? key = Registry.LocalMachine.CreateSubKey(ParametersKey);
            if (key is null)
            {
                return false;
            }

            key.SetValue(ValueName, unchecked((int)0xFFFFFFFF), RegistryValueKind.DWord);
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException or IOException)
        {
            return false;
        }

        RestartWebClientService();
        return true;
    }

    private static void RestartWebClientService()
    {
        RunNetCommand("stop", "webclient", "/y");
        RunNetCommand("start", "webclient");
    }

    private static void RunNetCommand(params string[] arguments)
    {
        try
        {
            var startInfo = new ProcessStartInfo(WindowsPaths.NetExe)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
            };

            foreach (string argument in arguments)
            {
                startInfo.ArgumentList.Add(argument);
            }

            using Process? process = Process.Start(startInfo);
            process?.WaitForExit(30_000);
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            // Best effort; a reboot also picks the new limit up.
        }
    }
}

/// <summary>The "Start with Windows" toggle, i.e. the HKCU Run key.</summary>
[SupportedOSPlatform("windows")]
internal static class AutoStart
{
    private const string RunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string ValueName = "GoldenCloud";

    public static bool IsEnabled()
    {
        try
        {
            using RegistryKey? key = Registry.CurrentUser.OpenSubKey(RunKey);
            return key?.GetValue(ValueName) is string value && !string.IsNullOrWhiteSpace(value);
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    public static bool SetEnabled(bool enabled)
    {
        try
        {
            using RegistryKey? key = Registry.CurrentUser.CreateSubKey(RunKey);
            if (key is null)
            {
                return false;
            }

            if (enabled)
            {
                key.SetValue(ValueName, "\"" + WindowsPaths.ExecutablePath + "\"", RegistryValueKind.String);
            }
            else
            {
                key.DeleteValue(ValueName, throwOnMissingValue: false);
            }

            return true;
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException or IOException)
        {
            return false;
        }
    }
}

/// <summary>Paths the app needs, resolved once.</summary>
[SupportedOSPlatform("windows")]
internal static class WindowsPaths
{
    /// <summary>
    /// This executable. Under single-file publish, Assembly.Location is empty,
    /// so Environment.ProcessPath is the only correct source.
    /// </summary>
    public static string ExecutablePath =>
        Environment.ProcessPath ?? System.Windows.Forms.Application.ExecutablePath;

    public static string InstallDirectory =>
        Path.GetDirectoryName(ExecutablePath) ?? Environment.CurrentDirectory;

    /// <summary>The bundled rclone.exe, which the installer places beside the tray app.</summary>
    public static string RcloneExe => Path.Combine(InstallDirectory, "rclone.exe");

    public static string NetExe =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), "net.exe");

    /// <summary>%LOCALAPPDATA%\GoldenCloud — settings and logs, never credentials.</summary>
    public static string DataDirectory
    {
        get
        {
            string path = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "GoldenCloud");
            Directory.CreateDirectory(path);
            return path;
        }
    }

    public static string SettingsFile => Path.Combine(DataDirectory, "settings.ini");

    public static string CacheDirectory
    {
        get
        {
            string path = Path.Combine(DataDirectory, "cache");
            Directory.CreateDirectory(path);
            return path;
        }
    }

    public static string LogFile => Path.Combine(DataDirectory, "mount.log");
}
