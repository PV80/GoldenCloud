using System;
using System.Linq;
using GoldenCloud.Core.Mounting;
using Xunit;

namespace GoldenCloud.Core.Tests;

/// <summary>
/// Mount-command generation for both strategies (D-005), and the hard rule that
/// the password never reaches the argument vector (D-006).
/// </summary>
public class MountCommandTests
{
    private const string ServerUrl = "https://cloud.acme.test/";
    private const string User = "alice";

    /// <summary>Distinctive enough that a substring search cannot false-negative.</summary>
    private const string Password = "Zx9-QuiteSecret-Passw0rd!";

    private static MountRequest Request(string? drive = null) =>
        new(ServerUrl, User, drive)
        {
            RclonePath = @"C:\Program Files\GoldenCloud\rclone.exe",
            NetPath = @"C:\Windows\System32\net.exe",
        };

    // ---- rclone -------------------------------------------------------------

    [Fact]
    public void Rclone_command_targets_the_bundled_binary_and_the_right_drive()
    {
        MountCommand command = new RcloneMountCommandBuilder().BuildMount(Request("G:"), "obscured-value");

        Assert.Equal(@"C:\Program Files\GoldenCloud\rclone.exe", command.FileName);
        Assert.Equal("mount", command.Arguments[0]);
        Assert.Equal(":webdav:", command.Arguments[1]);
        Assert.Equal("G:", command.Arguments[2]);
    }

    [Fact]
    public void Rclone_command_carries_the_url_vendor_and_user_as_flags()
    {
        MountCommand command = new RcloneMountCommandBuilder().BuildMount(Request(), "obscured-value");

        Assert.Contains("--webdav-url=https://cloud.acme.test/", command.Arguments);
        Assert.Contains("--webdav-vendor=other", command.Arguments);
        Assert.Contains("--webdav-user=alice", command.Arguments);
        Assert.Contains("--vfs-cache-mode=writes", command.Arguments);
        Assert.Contains("--volname=GoldenCloud", command.Arguments);

        // Windows-specific behaviour: network drive, no console window.
        Assert.Contains("--network-mode", command.Arguments);
        Assert.Contains("--no-console", command.Arguments);
    }

    [Fact]
    public void Rclone_uses_an_ad_hoc_remote_so_no_rclone_conf_is_ever_written()
    {
        MountCommand command = new RcloneMountCommandBuilder().BuildMount(Request(), "obscured-value");

        Assert.Contains(":webdav:", command.Arguments);
        Assert.DoesNotContain(command.Arguments, a => a.Contains("rclone.conf", StringComparison.OrdinalIgnoreCase));
        Assert.DoesNotContain(command.Arguments, a => a.StartsWith("--config", StringComparison.Ordinal));
    }

    [Fact]
    public void Rclone_optional_flags_appear_only_when_asked_for()
    {
        var withExtras = new MountRequest(ServerUrl, User, "H:")
        {
            RclonePath = "rclone.exe",
            CacheDirectory = @"C:\Users\alice\AppData\Local\GoldenCloud\cache",
            LogFile = @"C:\Users\alice\AppData\Local\GoldenCloud\rclone.log",
            LogLevel = "INFO",
        };

        MountCommand withCommand = new RcloneMountCommandBuilder().BuildMount(withExtras, "obscured-value");
        MountCommand withoutCommand = new RcloneMountCommandBuilder().BuildMount(Request(), "obscured-value");

        Assert.Contains(@"--cache-dir=C:\Users\alice\AppData\Local\GoldenCloud\cache", withCommand.Arguments);
        Assert.Contains(@"--log-file=C:\Users\alice\AppData\Local\GoldenCloud\rclone.log", withCommand.Arguments);
        Assert.Contains("--log-level=INFO", withCommand.Arguments);
        Assert.Equal("H:", withCommand.Arguments[2]);

        Assert.DoesNotContain(withoutCommand.Arguments, a => a.StartsWith("--cache-dir", StringComparison.Ordinal));
        Assert.DoesNotContain(withoutCommand.Arguments, a => a.StartsWith("--log-file", StringComparison.Ordinal));
        Assert.Contains("--log-level=NOTICE", withoutCommand.Arguments);
    }

    [Fact]
    public void Rclone_puts_the_obscured_password_in_the_environment_and_nowhere_else()
    {
        string obscured = RcloneObscure.Obscure(Password);

        MountCommand command = new RcloneMountCommandBuilder().BuildMount(Request(), obscured);

        Assert.Equal(obscured, command.Environment[RcloneMountCommandBuilder.PasswordEnvironmentVariable]);
        Assert.Equal(1, command.Environment.Count);
        Assert.Null(command.StandardInput);
    }

    [Fact]
    public void Rclone_argv_never_contains_the_password_in_any_form()
    {
        string obscured = RcloneObscure.Obscure(Password);

        MountCommand command = new RcloneMountCommandBuilder().BuildMount(Request(), obscured);

        AssertNoPasswordInArgv(command, Password, obscured);
    }

    [Fact]
    public void Rclone_has_no_unmount_command_because_the_process_is_the_mount()
    {
        Assert.Null(new RcloneMountCommandBuilder().BuildUnmount(Request()));
    }

    [Fact]
    public void Rclone_builder_rejects_an_empty_secret()
    {
        Assert.Throws<ArgumentException>(() => new RcloneMountCommandBuilder().BuildMount(Request(), string.Empty));
    }

    // ---- net use ------------------------------------------------------------

    [Fact]
    public void Net_use_command_maps_the_drive_to_the_server_url()
    {
        MountCommand command = new NetUseMountCommandBuilder().BuildMount(Request("G:"), Password);

        Assert.Equal(@"C:\Windows\System32\net.exe", command.FileName);
        Assert.Equal(
            new[] { "use", "G:", "https://cloud.acme.test/", "*", "/user:alice", "/persistent:no" },
            command.Arguments.ToArray());
    }

    [Fact]
    public void Net_use_asks_windows_to_prompt_and_feeds_the_password_on_stdin()
    {
        MountCommand command = new NetUseMountCommandBuilder().BuildMount(Request(), Password);

        // The '*' placeholder is what makes net.exe read the password from stdin.
        Assert.Contains(NetUseMountCommandBuilder.PasswordPlaceholder, command.Arguments);
        Assert.Equal(Password + "\r\n", command.StandardInput);
        Assert.Equal(0, command.Environment.Count);
    }

    [Fact]
    public void Net_use_argv_never_contains_the_password()
    {
        MountCommand command = new NetUseMountCommandBuilder().BuildMount(Request(), Password);

        AssertNoPasswordInArgv(command, Password);
    }

    [Fact]
    public void Net_use_can_be_asked_for_a_persistent_mapping()
    {
        var persistent = new MountRequest(ServerUrl, User, "G:") { NetPath = "net.exe", Persistent = true };

        MountCommand command = new NetUseMountCommandBuilder().BuildMount(persistent, Password);

        Assert.Contains("/persistent:yes", command.Arguments);
        Assert.DoesNotContain("/persistent:no", command.Arguments);
    }

    [Fact]
    public void Net_use_unmount_deletes_the_mapping_without_prompting()
    {
        MountCommand? command = new NetUseMountCommandBuilder().BuildUnmount(Request("G:"));

        Assert.NotNull(command);
        Assert.Equal(new[] { "use", "G:", "/delete", "/y" }, command!.Arguments.ToArray());
        Assert.Null(command.StandardInput);
        Assert.Equal(0, command.Environment.Count);
    }

    // ---- shared -------------------------------------------------------------

    [Fact]
    public void Strategies_identify_themselves()
    {
        Assert.Equal(MountStrategy.Rclone, new RcloneMountCommandBuilder().Strategy);
        Assert.Equal(MountStrategy.NetUse, new NetUseMountCommandBuilder().Strategy);
    }

    [Fact]
    public void The_redacted_command_line_is_safe_to_log_for_both_strategies()
    {
        string obscured = RcloneObscure.Obscure(Password);

        MountCommand rclone = new RcloneMountCommandBuilder().BuildMount(Request(), obscured);
        MountCommand netUse = new NetUseMountCommandBuilder().BuildMount(Request(), Password);

        Assert.DoesNotContain(Password, rclone.RedactedCommandLine, StringComparison.Ordinal);
        Assert.DoesNotContain(obscured, rclone.RedactedCommandLine, StringComparison.Ordinal);
        Assert.DoesNotContain(Password, netUse.RedactedCommandLine, StringComparison.Ordinal);
    }

    [Fact]
    public void Mount_request_rejects_an_insecure_or_malformed_server_url()
    {
        Assert.Throws<ArgumentException>(() => new MountRequest("http://cloud.acme.test/", User));
        Assert.Throws<ArgumentException>(() => new MountRequest("cloud.acme.test", User));
        Assert.Throws<ArgumentException>(() => new MountRequest(ServerUrl, "   "));
    }

    [Fact]
    public void Mount_request_normalises_the_url_and_drive_letter()
    {
        var request = new MountRequest("HTTPS://Cloud.ACME.test", "  alice  ", "g");

        Assert.Equal("https://cloud.acme.test/", request.ServerUrl);
        Assert.Equal("alice", request.Username);
        Assert.Equal("G:", request.DriveLetter);
    }

    /// <summary>
    /// The assertion the whole design exists to make: no form of the secret is
    /// anywhere in the argument vector, the file name, or the loggable line.
    /// </summary>
    private static void AssertNoPasswordInArgv(MountCommand command, params string[] secrets)
    {
        foreach (string secret in secrets)
        {
            Assert.False(
                command.ArgumentsContain(secret),
                "The secret leaked into argv: " + command.RedactedCommandLine);

            foreach (string argument in command.Arguments)
            {
                Assert.DoesNotContain(secret, argument, StringComparison.Ordinal);
            }

            Assert.DoesNotContain(secret, command.FileName, StringComparison.Ordinal);
            Assert.DoesNotContain(secret, command.RedactedCommandLine, StringComparison.Ordinal);
        }
    }
}
