using System;
using System.Runtime.Versioning;
using System.Threading;
using System.Windows.Forms;

namespace GoldenCloud.Tray;

[SupportedOSPlatform("windows")]
internal static class Program
{
    /// <summary>
    /// Re-entry switch: the tray app relaunches itself elevated with this
    /// argument to make the machine-wide WebClient registry change (D-005).
    /// </summary>
    internal const string FixWebClientLimitSwitch = "--fix-webclient-limit";

    /// <summary>Prints where things are and exits. For support calls.</summary>
    internal const string DiagnosticsSwitch = "--diagnostics";

    private const string SingleInstanceMutex = @"Local\GoldenCloud.Tray.SingleInstance";

    [STAThread]
    private static int Main(string[] args)
    {
        if (HasSwitch(args, FixWebClientLimitSwitch))
        {
            // Runs elevated, does one thing, exits. No UI, no message loop.
            return WebClientLimit.ApplyFromElevatedProcess() ? 0 : 1;
        }

        if (HasSwitch(args, DiagnosticsSwitch))
        {
            ShowDiagnostics();
            return 0;
        }

        // One tray icon per signed-in user, no matter how many times the
        // shortcut is clicked.
        using var instanceMutex = new Mutex(initiallyOwned: true, SingleInstanceMutex, out bool createdNew);
        if (!createdNew)
        {
            return 0;
        }

        try
        {
            Application.SetHighDpiMode(HighDpiMode.PerMonitorV2);
        }
        catch (Exception ex) when (ex is InvalidOperationException or NotSupportedException)
        {
            // Already set by the manifest on some Windows builds; harmless.
        }

        Application.EnableVisualStyles();
        Application.SetCompatibleTextRenderingDefault(false);

        Application.ThreadException += (_, e) =>
        {
            MountSupervisor.Log("unhandled UI exception: " + e.Exception);
            MessageBox.Show(
                "GoldenCloud hit an unexpected problem:\r\n\r\n" + e.Exception.Message,
                "GoldenCloud",
                MessageBoxButtons.OK,
                MessageBoxIcon.Error);
        };

        AppDomain.CurrentDomain.UnhandledException += (_, e) =>
            MountSupervisor.Log("unhandled exception: " + e.ExceptionObject);

        using var context = new TrayApplicationContext();
        Application.Run(context);

        return 0;
    }

    private static bool HasSwitch(string[] args, string name)
    {
        foreach (string argument in args)
        {
            if (string.Equals(argument, name, StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }
        }

        return false;
    }

    private static void ShowDiagnostics()
    {
        bool winFsp = WinFspDetector.IsInstalled(out string? winFspDirectory);
        long? limit = WebClientLimit.ReadLimitBytes();
        AppSettings settings = AppSettings.Load();

        string report =
            "GoldenCloud client diagnostics" + Environment.NewLine +
            "------------------------------" + Environment.NewLine +
            "Server URL:            " + BuildConfig.ServerUrl + Environment.NewLine +
            "Build configured:      " + BuildConfig.IsConfigured + Environment.NewLine +
            "Executable:            " + WindowsPaths.ExecutablePath + Environment.NewLine +
            "rclone.exe present:    " + System.IO.File.Exists(WindowsPaths.RcloneExe) + Environment.NewLine +
            "WinFsp installed:      " + winFsp + Environment.NewLine +
            "WinFsp directory:      " + (winFspDirectory ?? "(unknown)") + Environment.NewLine +
            "Mount strategy:        " + (winFsp ? "rclone + WinFsp" : "net use (Windows WebDAV)") + Environment.NewLine +
            "WebDAV size limit:     " + (limit?.ToString() ?? "(default 50,000,000)") + Environment.NewLine +
            "Drive letter:          " + settings.DriveLetter + Environment.NewLine +
            "Start with Windows:    " + AutoStart.IsEnabled() + Environment.NewLine +
            "Data directory:        " + WindowsPaths.DataDirectory + Environment.NewLine +
            "Credentials:           Windows Credential Manager, generic target \"GoldenCloud\"";

        MessageBox.Show(report, "GoldenCloud diagnostics", MessageBoxButtons.OK, MessageBoxIcon.Information);
    }
}
