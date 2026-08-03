using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;

namespace GoldenCloud.Core.Mounting;

/// <summary>Which of the two mount strategies a command belongs to (D-005).</summary>
public enum MountStrategy
{
    /// <summary>Bundled rclone.exe + WinFsp. Preferred.</summary>
    Rclone = 0,

    /// <summary>Windows' built-in WebDAV redirector via <c>net use</c>. Fallback.</summary>
    NetUse = 1,
}

/// <summary>
/// A fully-formed child-process invocation: what to run, the argument vector,
/// the extra environment variables, and anything to feed to standard input.
///
/// The type exists so that "the password never appears in argv" is a property a
/// unit test can assert on, rather than a property of scattered string
/// concatenation inside a UI event handler.
/// </summary>
public sealed class MountCommand
{
    public MountCommand(
        string fileName,
        IReadOnlyList<string> arguments,
        IReadOnlyDictionary<string, string>? environment = null,
        string? standardInput = null)
    {
        if (string.IsNullOrWhiteSpace(fileName))
        {
            throw new ArgumentException("File name must not be empty.", nameof(fileName));
        }

        FileName = fileName;
        Arguments = arguments ?? throw new ArgumentNullException(nameof(arguments));
        Environment = environment ?? new ReadOnlyDictionary<string, string>(new Dictionary<string, string>(StringComparer.Ordinal));
        StandardInput = standardInput;
    }

    public string FileName { get; }

    public IReadOnlyList<string> Arguments { get; }

    /// <summary>
    /// Environment variables to add to the child process. Secrets go here, never
    /// in <see cref="Arguments"/>: on Windows a process command line is readable
    /// by any user via WMI, but its environment block is not.
    /// </summary>
    public IReadOnlyDictionary<string, string> Environment { get; }

    /// <summary>Text written to the child's stdin, if any. Secrets may go here.</summary>
    public string? StandardInput { get; }

    /// <summary>True when <paramref name="needle"/> occurs in any argument.</summary>
    public bool ArgumentsContain(string needle)
    {
        if (string.IsNullOrEmpty(needle))
        {
            return false;
        }

        return Arguments.Any(a => a is not null && a.Contains(needle, StringComparison.Ordinal));
    }

    /// <summary>
    /// A log-safe rendering. Safe by construction because arguments never hold a
    /// secret, but named so that a future reader does not have to check.
    /// </summary>
    public string RedactedCommandLine =>
        Arguments.Count == 0 ? FileName : FileName + " " + string.Join(" ", Arguments);

    public override string ToString() => RedactedCommandLine;
}
