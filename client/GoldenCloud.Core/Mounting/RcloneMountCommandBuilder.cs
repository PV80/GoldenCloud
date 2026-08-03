using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace GoldenCloud.Core.Mounting;

/// <summary>
/// Builds <c>rclone mount</c> against an ad-hoc WebDAV remote (D-005, primary).
///
/// The remote is spelled <c>:webdav:</c> — rclone's "connection string" form —
/// so no rclone.conf is created, read or written anywhere on disk. The backend
/// options are supplied as <c>--webdav-*</c> flags, except the password, which
/// is supplied through the environment variable that corresponds to
/// <c>--webdav-pass</c>. rclone maps every flag <c>--foo-bar</c> to the
/// environment variable <c>RCLONE_FOO_BAR</c>.
/// </summary>
public sealed class RcloneMountCommandBuilder : IMountCommandBuilder
{
    /// <summary>Environment variable equivalent of the <c>--webdav-pass</c> flag.</summary>
    public const string PasswordEnvironmentVariable = "RCLONE_WEBDAV_PASS";

    public MountStrategy Strategy => MountStrategy.Rclone;

    public MountCommand BuildMount(MountRequest request, string obscuredPassword)
    {
        if (request is null)
        {
            throw new ArgumentNullException(nameof(request));
        }

        if (string.IsNullOrEmpty(obscuredPassword))
        {
            throw new ArgumentException("Obscured password must not be empty.", nameof(obscuredPassword));
        }

        var arguments = new List<string>
        {
            "mount",
            ":webdav:",
            request.DriveLetter,
            "--webdav-url=" + request.ServerUrl,
            "--webdav-vendor=other",
            "--webdav-user=" + request.Username,
            "--vfs-cache-mode=" + request.VfsCacheMode,
            "--dir-cache-time=10s",
            "--volname=" + request.VolumeLabel,

            // Windows-only rclone flags: present the mount as a network drive and
            // never flash a console window at a member of staff.
            "--network-mode",
            "--no-console",

            "--log-level=" + request.LogLevel,
        };

        if (!string.IsNullOrWhiteSpace(request.CacheDirectory))
        {
            arguments.Add("--cache-dir=" + request.CacheDirectory);
        }

        if (!string.IsNullOrWhiteSpace(request.LogFile))
        {
            arguments.Add("--log-file=" + request.LogFile);
        }

        var environment = new Dictionary<string, string>(StringComparer.Ordinal)
        {
            [PasswordEnvironmentVariable] = obscuredPassword,
        };

        return new MountCommand(
            request.RclonePath,
            new ReadOnlyCollection<string>(arguments),
            new ReadOnlyDictionary<string, string>(environment),
            standardInput: null);
    }

    /// <summary>
    /// rclone has no "unmount" verb on Windows; the mount ends when the child
    /// process ends. The supervisor terminates it instead.
    /// </summary>
    public MountCommand? BuildUnmount(MountRequest request) => null;
}
