using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace GoldenCloud.Core.Mounting;

/// <summary>
/// Builds <c>rclone mount</c> for a chunker-wrapped WebDAV remote (D-005
/// primary strategy, D-028 chunking).
///
/// Two remotes are defined entirely through <c>RCLONE_CONFIG_&lt;NAME&gt;_&lt;KEY&gt;</c>
/// environment variables, so no rclone.conf is created, read or written
/// anywhere on disk:
///
/// <code>
///   gcwebdav  — the real WebDAV backend (url, vendor, user, obscured pass)
///   gcdrive   — a chunker overlay on gcwebdav: that splits every file into
///               chunks below Cloudflare's 100 MB proxied-request cap
/// </code>
///
/// The mount targets <c>gcdrive:</c>. Without the overlay, rclone uploads each
/// file as a single PUT and anything over the cap dies at Cloudflare's edge
/// with a 413 (D-024); with it, multi-gigabyte files upload as a series of
/// sub-cap chunks and are reassembled transparently on read. The password is
/// only ever placed in the environment, never in an argument (D-006).
/// </summary>
public sealed class RcloneMountCommandBuilder : IMountCommandBuilder
{
    /// <summary>Name of the underlying WebDAV remote.</summary>
    public const string WebDavRemoteName = "gcwebdav";

    /// <summary>Name of the chunker overlay remote the drive actually mounts.</summary>
    public const string DriveRemoteName = "gcdrive";

    /// <summary>Environment variable carrying the obscured WebDAV password.</summary>
    public const string PasswordEnvironmentVariable = "RCLONE_CONFIG_GCWEBDAV_PASS";

    /// <summary>
    /// Chunk size for uploads. 95 MiB is 99,614,720 bytes — safely under
    /// Cloudflare's 100,000,000-byte proxied request-body cap (D-024), with
    /// headroom for request overhead.
    /// </summary>
    public const string ChunkSize = "95Mi";

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
            DriveRemoteName + ":",
            request.DriveLetter,
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
            // The WebDAV backend, exactly as the old ad-hoc remote but named.
            ["RCLONE_CONFIG_GCWEBDAV_TYPE"] = "webdav",
            ["RCLONE_CONFIG_GCWEBDAV_URL"] = request.ServerUrl,
            ["RCLONE_CONFIG_GCWEBDAV_VENDOR"] = "other",
            ["RCLONE_CONFIG_GCWEBDAV_USER"] = request.Username,
            [PasswordEnvironmentVariable] = obscuredPassword,

            // The chunker overlay the drive mounts. Files at or below ChunkSize
            // are stored as-is, so existing un-chunked files on the share read
            // back transparently.
            ["RCLONE_CONFIG_GCDRIVE_TYPE"] = "chunker",
            ["RCLONE_CONFIG_GCDRIVE_REMOTE"] = WebDavRemoteName + ":",
            ["RCLONE_CONFIG_GCDRIVE_CHUNK_SIZE"] = ChunkSize,
            // Fail a read loudly if a chunk is missing rather than returning a
            // silently truncated file.
            ["RCLONE_CONFIG_GCDRIVE_FAIL_HARD"] = "true",
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
