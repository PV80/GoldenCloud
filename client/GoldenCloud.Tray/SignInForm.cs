using System;
using System.Drawing;
using System.Runtime.Versioning;
using System.Threading;
using System.Threading.Tasks;
using System.Windows.Forms;
using GoldenCloud.Core;
using GoldenCloud.Core.WebDav;

namespace GoldenCloud.Tray;

/// <summary>
/// Username and password, nothing else. The server address is baked in at build
/// time (D-007), so staff never see or type it.
/// </summary>
[SupportedOSPlatform("windows")]
internal sealed class SignInForm : Form
{
    private readonly AccountService _account;
    private readonly AppSettings _settings;

    private readonly TextBox _username = new();
    private readonly TextBox _password = new();
    private readonly Button _signIn = new();
    private readonly Button _cancel = new();
    private readonly Label _status = new();
    private readonly Label _serverLabel = new();

    private CancellationTokenSource? _inFlight;

    public SignInForm(AccountService account, AppSettings settings)
    {
        _account = account;
        _settings = settings;

        Text = "Sign in to GoldenCloud";
        FormBorderStyle = FormBorderStyle.FixedDialog;
        MaximizeBox = false;
        MinimizeBox = false;
        StartPosition = FormStartPosition.CenterScreen;
        ShowInTaskbar = true;
        ClientSize = new Size(420, _account.IsUnconfiguredBuild ? 260 : 210);
        AutoScaleMode = AutoScaleMode.Dpi;
        Padding = new Padding(16);

        int top = 16;

        if (_account.IsUnconfiguredBuild)
        {
            var banner = new Label
            {
                Text = "UNCONFIGURED BUILD\r\n" +
                       "No server address was compiled into this installer, so it cannot be used.\r\n" +
                       "Set GOLDENCLOUD_SERVER_URL and rebuild. See PROGRESS.md, human gate 2.",
                BackColor = Color.FromArgb(0xC8, 0x30, 0x30),
                ForeColor = Color.White,
                Font = new Font(this.Font, FontStyle.Bold),
                TextAlign = ContentAlignment.MiddleLeft,
                Location = new Point(16, top),
                Size = new Size(388, 64),
                Padding = new Padding(8, 4, 8, 4),
                AutoSize = false,
            };

            Controls.Add(banner);
            top += 76;
        }

        _serverLabel.Text = "Server: " + _account.ServerUrl;
        _serverLabel.Location = new Point(16, top);
        _serverLabel.Size = new Size(388, 20);
        _serverLabel.ForeColor = SystemColors.GrayText;
        _serverLabel.AutoSize = false;
        Controls.Add(_serverLabel);
        top += 28;

        var usernameLabel = new Label
        {
            Text = "&Username",
            Location = new Point(16, top + 3),
            Size = new Size(90, 20),
            AutoSize = false,
        };
        Controls.Add(usernameLabel);

        _username.Location = new Point(110, top);
        _username.Size = new Size(294, 23);
        _username.Text = _settings.LastUsername;
        Controls.Add(_username);
        top += 32;

        var passwordLabel = new Label
        {
            Text = "&Password",
            Location = new Point(16, top + 3),
            Size = new Size(90, 20),
            AutoSize = false,
        };
        Controls.Add(passwordLabel);

        _password.Location = new Point(110, top);
        _password.Size = new Size(294, 23);
        _password.UseSystemPasswordChar = true;
        Controls.Add(_password);
        top += 34;

        _status.Location = new Point(16, top);
        _status.Size = new Size(388, 36);
        _status.AutoSize = false;
        _status.ForeColor = SystemColors.GrayText;
        Controls.Add(_status);
        top += 40;

        _signIn.Text = "Sign in";
        _signIn.Location = new Point(228, top);
        _signIn.Size = new Size(84, 28);
        _signIn.Click += OnSignInClicked;
        _signIn.Enabled = !_account.IsUnconfiguredBuild;
        Controls.Add(_signIn);

        _cancel.Text = "Cancel";
        _cancel.Location = new Point(320, top);
        _cancel.Size = new Size(84, 28);
        _cancel.Click += (_, _) => Close();
        Controls.Add(_cancel);

        AcceptButton = _signIn;
        CancelButton = _cancel;

        if (_account.IsUnconfiguredBuild)
        {
            _username.Enabled = false;
            _password.Enabled = false;
            _status.Text = "This build refuses to store credentials.";
            _status.ForeColor = Color.FromArgb(0xC8, 0x30, 0x30);
        }

        FormClosed += (_, _) => _inFlight?.Cancel();
    }

    /// <summary>True when the user signed in successfully.</summary>
    public bool SignedIn { get; private set; }

    private async void OnSignInClicked(object? sender, EventArgs e)
    {
        if (_account.IsUnconfiguredBuild)
        {
            return;
        }

        string username = _username.Text.Trim();
        string password = _password.Text;

        SetBusy(true, "Checking with the server...");

        _inFlight?.Cancel();
        _inFlight = new CancellationTokenSource(TimeSpan.FromSeconds(45));

        SignInResult result;
        try
        {
            result = await _account.SignInAsync(
                username,
                password,
                remember: true,
                cancellationToken: _inFlight.Token);
        }
        catch (Exception ex)
        {
            SetBusy(false, "Sign-in failed: " + ex.Message, error: true);
            return;
        }

        if (result.Succeeded)
        {
            _settings.LastUsername = username;
            _settings.Save();
            SignedIn = true;
            DialogResult = DialogResult.OK;
            Close();
            return;
        }

        SetBusy(false, result.Message, error: true);
        _password.SelectAll();
        _password.Focus();
    }

    private void SetBusy(bool busy, string message, bool error = false)
    {
        _signIn.Enabled = !busy && !_account.IsUnconfiguredBuild;
        _cancel.Enabled = true;
        _username.Enabled = !busy && !_account.IsUnconfiguredBuild;
        _password.Enabled = !busy && !_account.IsUnconfiguredBuild;
        _status.Text = message;
        _status.ForeColor = error ? Color.FromArgb(0xC8, 0x30, 0x30) : SystemColors.GrayText;
        UseWaitCursor = busy;
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            _inFlight?.Dispose();
            _inFlight = null;
        }

        base.Dispose(disposing);
    }
}
