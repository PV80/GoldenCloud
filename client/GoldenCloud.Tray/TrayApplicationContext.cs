using System;
using System.Diagnostics;
using System.Net.Http;
using System.Runtime.Versioning;
using System.Windows.Forms;
using GoldenCloud.Core;
using GoldenCloud.Core.Credentials;
using GoldenCloud.Core.Mounting;
using GoldenCloud.Core.WebDav;

namespace GoldenCloud.Tray;

/// <summary>
/// The tray icon and its menu. Menu, in order: Open drive, Reconnect, Sign out,
/// Start with Windows, Quit.
/// </summary>
[SupportedOSPlatform("windows")]
internal sealed class TrayApplicationContext : ApplicationContext
{
    private readonly AppSettings _settings;
    private readonly ICredentialStore _credentialStore;
    private readonly HttpClient _http;
    private readonly AccountService _account;
    private readonly MountSupervisor _supervisor;

    private readonly NotifyIcon _notifyIcon;
    private readonly ContextMenuStrip _menu;
    private readonly ToolStripMenuItem _openDriveItem;
    private readonly ToolStripMenuItem _reconnectItem;
    private readonly ToolStripMenuItem _signOutItem;
    private readonly ToolStripMenuItem _startWithWindowsItem;
    private readonly ToolStripMenuItem _quitItem;

    /// <summary>Owns the UI thread's handle so background events can marshal onto it.</summary>
    private readonly Control _uiThreadMarshal;

    private bool _webClientPromptShown;
    private bool _disposed;

    public TrayApplicationContext()
    {
        _settings = AppSettings.Load();
        _credentialStore = new WindowsCredentialStore();

        _http = new HttpClient(new HttpClientHandler { AllowAutoRedirect = false })
        {
            Timeout = TimeSpan.FromSeconds(30),
        };
        _http.DefaultRequestHeaders.UserAgent.ParseAdd("GoldenCloud-Client/1.0");

        _account = new AccountService(BuildConfig.ServerUrl, _credentialStore, new WebDavProbe(_http));

        _uiThreadMarshal = new Control();
        _ = _uiThreadMarshal.Handle; // force handle creation on the UI thread

        _supervisor = new MountSupervisor(
            BuildConfig.ServerUrl,
            _settings,
            () => _credentialStore.Read(CredentialTargets.Default),
            new RcloneProcessObscurer(WindowsPaths.RcloneExe));
        _supervisor.StatusChanged += OnStatusChanged;

        _openDriveItem = new ToolStripMenuItem("Open drive", null, (_, _) => OpenDrive());
        _reconnectItem = new ToolStripMenuItem("Reconnect", null, (_, _) => Reconnect());
        _signOutItem = new ToolStripMenuItem("Sign out", null, (_, _) => SignOut());
        _startWithWindowsItem = new ToolStripMenuItem("Start with Windows", null, (_, _) => ToggleStartWithWindows())
        {
            CheckOnClick = false,
            Checked = AutoStart.IsEnabled(),
        };
        _quitItem = new ToolStripMenuItem("Quit", null, (_, _) => Quit());

        _menu = new ContextMenuStrip();
        _menu.Items.AddRange(new ToolStripItem[]
        {
            _openDriveItem,
            _reconnectItem,
            _signOutItem,
            _startWithWindowsItem,
            _quitItem,
        });

        _notifyIcon = new NotifyIcon
        {
            Icon = TrayIcons.ForState(MountState.SignedOut),
            Text = "GoldenCloud",
            ContextMenuStrip = _menu,
            Visible = true,
        };
        _notifyIcon.DoubleClick += (_, _) => OpenDrive();

        MountSupervisor.Log("tray started; server=" + BuildConfig.ServerUrl + " configured=" + BuildConfig.IsConfigured);

        // Deferred so the message loop is running before any dialog appears.
        _uiThreadMarshal.BeginInvoke(new Action(FirstRun));
    }

    // ---- startup ------------------------------------------------------------

    private void FirstRun()
    {
        if (!BuildConfig.IsConfigured || _account.IsUnconfiguredBuild)
        {
            ShowSignInDialog();
            return;
        }

        WarnAboutRedirectorLimitIfNeeded();

        if (!_account.IsSignedIn)
        {
            ShowSignInDialog();
        }

        _supervisor.Start();
    }

    private void ShowSignInDialog()
    {
        using var form = new SignInForm(_account, _settings);
        form.ShowDialog();

        if (form.SignedIn)
        {
            _supervisor.RequestReconnect();
            _supervisor.Start();
            Notify("GoldenCloud", "Signed in. Connecting " + _settings.DriveLetter + "...");
        }
    }

    /// <summary>
    /// The fallback strategy hits Windows' 50 MB WebDAV download cap. Raising it
    /// is a machine-wide change, so it is offered, explained, and only made with
    /// consent (D-005).
    /// </summary>
    private void WarnAboutRedirectorLimitIfNeeded()
    {
        if (_webClientPromptShown)
        {
            return;
        }

        if (WinFspDetector.IsInstalled() && System.IO.File.Exists(WindowsPaths.RcloneExe))
        {
            return; // rclone path is in use; the redirector limit does not apply.
        }

        if (!WebClientLimit.IsRestrictive())
        {
            return;
        }

        _webClientPromptShown = true;

        DialogResult answer = MessageBox.Show(
            "WinFsp is not installed on this PC, so GoldenCloud will use the Windows " +
            "built-in WebDAV support instead.\r\n\r\n" +
            "Windows refuses to download any file larger than 50 MB over WebDAV until a " +
            "registry limit is raised.\r\n\r\n" +
            "Fixing it:\r\n" +
            "  • changes a setting for EVERY user of this PC, not just you;\r\n" +
            "  • needs administrator rights, so Windows will ask for permission;\r\n" +
            "  • restarts the WebClient service, which briefly disconnects any\r\n" +
            "    WebDAV drives that are open.\r\n\r\n" +
            "Raise the limit now?",
            "GoldenCloud — large files are blocked by Windows",
            MessageBoxButtons.YesNo,
            MessageBoxIcon.Warning,
            MessageBoxDefaultButton.Button2);

        if (answer != DialogResult.Yes)
        {
            MountSupervisor.Log("user declined the WebClient file size limit fix");
            return;
        }

        if (RelaunchElevatedForWebClientFix())
        {
            Notify("GoldenCloud", "The 50 MB WebDAV limit has been raised.");
        }
        else
        {
            MessageBox.Show(
                "The limit was not changed. Files larger than 50 MB will fail to download " +
                "until it is raised, or until WinFsp is installed.",
                "GoldenCloud",
                MessageBoxButtons.OK,
                MessageBoxIcon.Information);
        }
    }

    private static bool RelaunchElevatedForWebClientFix()
    {
        try
        {
            var startInfo = new ProcessStartInfo(WindowsPaths.ExecutablePath)
            {
                UseShellExecute = true,
                Verb = "runas", // triggers the UAC prompt
                Arguments = Program.FixWebClientLimitSwitch,
            };

            using Process? process = Process.Start(startInfo);
            if (process is null)
            {
                return false;
            }

            process.WaitForExit(120_000);
            return process.HasExited && process.ExitCode == 0 && !WebClientLimit.IsRestrictive();
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            // Win32Exception 1223 is "the user cancelled the UAC prompt".
            MountSupervisor.Log("elevation for the WebClient fix failed: " + ex.Message);
            return false;
        }
    }

    // ---- menu actions -------------------------------------------------------

    private void OpenDrive()
    {
        try
        {
            string root = DriveLetters.ToRootPath(_settings.DriveLetter);

            // Letting the shell open the path is simpler and safer than building
            // an explorer.exe command line.
            var startInfo = new ProcessStartInfo
            {
                FileName = root,
                UseShellExecute = true,
            };

            using Process? explorer = Process.Start(startInfo);
            _ = explorer;
        }
        catch (Exception ex)
        {
            MountSupervisor.Log("open drive failed: " + ex.Message);
            Notify("GoldenCloud", "The drive is not available yet.");
        }
    }

    private void Reconnect()
    {
        if (!_account.IsSignedIn)
        {
            ShowSignInDialog();
            return;
        }

        Notify("GoldenCloud", "Reconnecting " + _settings.DriveLetter + "...");
        _supervisor.RequestReconnect();
        _supervisor.Start();
    }

    private void SignOut()
    {
        DialogResult answer = MessageBox.Show(
            "Sign out and disconnect " + _settings.DriveLetter + "?\r\n\r\n" +
            "Your saved password will be deleted from Windows Credential Manager.",
            "GoldenCloud",
            MessageBoxButtons.YesNo,
            MessageBoxIcon.Question);

        if (answer != DialogResult.Yes)
        {
            return;
        }

        _supervisor.Stop();

        bool deleted = _account.SignOut();
        MountSupervisor.Log("signed out; credential deleted=" + deleted);

        UpdateUi(new MountStatus(MountState.SignedOut, "Signed out.", null, _settings.DriveLetter));
        Notify("GoldenCloud", "Signed out. Your password has been removed from this PC.");
    }

    private void ToggleStartWithWindows()
    {
        bool wanted = !_startWithWindowsItem.Checked;

        if (!AutoStart.SetEnabled(wanted))
        {
            MessageBox.Show(
                "Windows would not let GoldenCloud change the startup setting.",
                "GoldenCloud",
                MessageBoxButtons.OK,
                MessageBoxIcon.Warning);
        }

        _startWithWindowsItem.Checked = AutoStart.IsEnabled();
    }

    private void Quit()
    {
        _supervisor.Stop();
        _notifyIcon.Visible = false;
        ExitThread();
    }

    // ---- status -------------------------------------------------------------

    private void OnStatusChanged(MountStatus status)
    {
        try
        {
            if (_uiThreadMarshal.IsDisposed || !_uiThreadMarshal.IsHandleCreated)
            {
                return;
            }

            _uiThreadMarshal.BeginInvoke(new Action(() => UpdateUi(status)));
        }
        catch (Exception ex) when (ex is ObjectDisposedException or InvalidOperationException)
        {
            // The UI is going away.
        }
    }

    private void UpdateUi(MountStatus status)
    {
        _notifyIcon.Icon = TrayIcons.ForState(status.State);

        string strategy = status.Strategy switch
        {
            MountStrategy.Rclone => " (rclone)",
            MountStrategy.NetUse => " (Windows WebDAV)",
            _ => string.Empty,
        };

        // NotifyIcon.Text is capped at 63 characters by the shell.
        string text = "GoldenCloud — " + status.Message + strategy;
        _notifyIcon.Text = text.Length > 63 ? text.Substring(0, 60) + "..." : text;

        _openDriveItem.Enabled = status.State == MountState.Connected;
        _signOutItem.Enabled = _account.IsSignedIn;
        _reconnectItem.Enabled = _account.IsSignedIn;
    }

    private void Notify(string title, string message)
    {
        try
        {
            _notifyIcon.BalloonTipTitle = title;
            _notifyIcon.BalloonTipText = message;
            _notifyIcon.ShowBalloonTip(5000);
        }
        catch (Exception ex) when (ex is InvalidOperationException or ObjectDisposedException)
        {
            // Notifications are cosmetic.
        }
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing && !_disposed)
        {
            _disposed = true;

            _supervisor.StatusChanged -= OnStatusChanged;
            _supervisor.Dispose();

            _notifyIcon.Visible = false;
            _notifyIcon.Dispose();
            _menu.Dispose();
            _uiThreadMarshal.Dispose();
            _http.Dispose();
        }

        base.Dispose(disposing);
    }
}
