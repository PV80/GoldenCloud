using System;
using System.Diagnostics;
using System.IO;
using System.Runtime.Versioning;
using GoldenCloud.Core.Mounting;

namespace GoldenCloud.Tray;

/// <summary>
/// Obscures the password by asking the bundled rclone.exe to do it, feeding the
/// password on standard input so it never appears in a command line (D-006).
///
/// If rclone cannot be run — missing, blocked, or a future release that changes
/// the sub-command — this falls back to the managed implementation in
/// GoldenCloud.Core, so a mount is never lost to a tooling detail.
/// </summary>
[SupportedOSPlatform("windows")]
internal sealed class RcloneProcessObscurer : IPasswordObscurer
{
    private readonly string _rclonePath;
    private readonly IPasswordObscurer _fallback = new ManagedPasswordObscurer();

    public RcloneProcessObscurer(string rclonePath)
    {
        _rclonePath = rclonePath;
    }

    /// <summary>Set when the fallback had to be used. Surfaced in the log only.</summary>
    public bool UsedFallback { get; private set; }

    public string Obscure(string plaintext)
    {
        string? viaRclone = TryObscureWithRclone(plaintext);
        if (!string.IsNullOrEmpty(viaRclone))
        {
            UsedFallback = false;
            return viaRclone;
        }

        UsedFallback = true;
        return _fallback.Obscure(plaintext);
    }

    private string? TryObscureWithRclone(string plaintext)
    {
        try
        {
            if (string.IsNullOrEmpty(_rclonePath) || !File.Exists(_rclonePath))
            {
                return null;
            }

            var startInfo = new ProcessStartInfo(_rclonePath)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
            };

            // "-" tells rclone to read the value from standard input.
            startInfo.ArgumentList.Add("obscure");
            startInfo.ArgumentList.Add("-");

            using Process? process = Process.Start(startInfo);
            if (process is null)
            {
                return null;
            }

            process.StandardInput.Write(plaintext);
            process.StandardInput.Write('\n');
            process.StandardInput.Flush();
            process.StandardInput.Close();

            string output = process.StandardOutput.ReadToEnd();
            process.StandardError.ReadToEnd();

            if (!process.WaitForExit(15_000))
            {
                TryKill(process);
                return null;
            }

            if (process.ExitCode != 0)
            {
                return null;
            }

            string trimmed = output.Trim();

            // Sanity-check the shape before trusting it: base64url, no padding.
            if (trimmed.Length < 22 || trimmed.IndexOfAny(new[] { ' ', '\n', '\r', '=', '+', '/' }) >= 0)
            {
                return null;
            }

            return trimmed;
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception
                                      or InvalidOperationException
                                      or IOException
                                      or ObjectDisposedException)
        {
            return null;
        }
    }

    private static void TryKill(Process process)
    {
        try
        {
            process.Kill(entireProcessTree: true);
        }
        catch (Exception ex) when (ex is InvalidOperationException
                                      or System.ComponentModel.Win32Exception
                                      or NotSupportedException)
        {
            // Already gone.
        }
    }
}
