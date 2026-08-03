using System;

namespace GoldenCloud.Core.Mounting;

/// <summary>
/// Everything needed to build a mount command except the secret. Immutable so a
/// command builder cannot be handed a half-populated request.
/// </summary>
public sealed class MountRequest
{
    public MountRequest(string serverUrl, string username, string? driveLetter = null)
    {
        ServerUrlValidationResult validation = ServerUrlValidator.Validate(serverUrl);
        if (!validation.IsValid || validation.NormalisedUrl is null)
        {
            throw new ArgumentException(validation.Message, nameof(serverUrl));
        }

        if (string.IsNullOrWhiteSpace(username))
        {
            throw new ArgumentException("Username must not be empty.", nameof(username));
        }

        ServerUrl = validation.NormalisedUrl;
        Username = username.Trim();
        DriveLetter = DriveLetters.Normalise(string.IsNullOrWhiteSpace(driveLetter) ? DriveLetters.Default : driveLetter);
    }

    /// <summary>Always normalised, always ends in '/'.</summary>
    public string ServerUrl { get; }

    public string Username { get; }

    /// <summary>Normalised, e.g. "G:".</summary>
    public string DriveLetter { get; }

    /// <summary>Volume label shown in File Explorer.</summary>
    public string VolumeLabel { get; init; } = "GoldenCloud";

    /// <summary>Path to the bundled rclone.exe.</summary>
    public string RclonePath { get; init; } = "rclone.exe";

    /// <summary>Path to net.exe. Resolved to %SystemRoot%\System32\net.exe by the tray app.</summary>
    public string NetPath { get; init; } = "net.exe";

    /// <summary>rclone --vfs-cache-mode. "writes" is the safe default for Office files.</summary>
    public string VfsCacheMode { get; init; } = "writes";

    /// <summary>rclone --cache-dir. Null leaves rclone's own default in place.</summary>
    public string? CacheDirectory { get; init; }

    /// <summary>
    /// Whether Windows should remember the mapping across reboots. False by
    /// default: the tray app re-mounts on start, and a persistent mapping that
    /// Windows restores without credentials shows up as a broken drive.
    /// </summary>
    public bool Persistent { get; init; }

    /// <summary>rclone --log-level.</summary>
    public string LogLevel { get; init; } = "NOTICE";

    /// <summary>rclone --log-file. Null means log to stderr, which the tray app captures.</summary>
    public string? LogFile { get; init; }
}
