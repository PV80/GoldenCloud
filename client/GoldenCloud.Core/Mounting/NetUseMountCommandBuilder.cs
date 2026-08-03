using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace GoldenCloud.Core.Mounting;

/// <summary>
/// Builds <c>net use</c> against the Windows WebDAV redirector (D-005, fallback
/// for machines where WinFsp is absent).
///
/// The password is passed as the literal <c>*</c> placeholder, which makes
/// net.exe prompt for it and read it from standard input. That keeps it out of
/// the argument vector, which on Windows is readable by other users through WMI.
/// </summary>
public sealed class NetUseMountCommandBuilder : IMountCommandBuilder
{
    /// <summary>The placeholder that makes net.exe read the password from stdin.</summary>
    public const string PasswordPlaceholder = "*";

    public MountStrategy Strategy => MountStrategy.NetUse;

    public MountCommand BuildMount(MountRequest request, string password)
    {
        if (request is null)
        {
            throw new ArgumentNullException(nameof(request));
        }

        if (string.IsNullOrEmpty(password))
        {
            throw new ArgumentException("Password must not be empty.", nameof(password));
        }

        var arguments = new List<string>
        {
            "use",
            request.DriveLetter,
            request.ServerUrl,
            PasswordPlaceholder,
            "/user:" + request.Username,
            request.Persistent ? "/persistent:yes" : "/persistent:no",
        };

        // net.exe reads the password as a console line, so it needs the CRLF.
        return new MountCommand(
            request.NetPath,
            new ReadOnlyCollection<string>(arguments),
            environment: null,
            standardInput: password + "\r\n");
    }

    public MountCommand? BuildUnmount(MountRequest request)
    {
        if (request is null)
        {
            throw new ArgumentNullException(nameof(request));
        }

        var arguments = new List<string>
        {
            "use",
            request.DriveLetter,
            "/delete",
            "/y",
        };

        return new MountCommand(request.NetPath, new ReadOnlyCollection<string>(arguments));
    }
}
