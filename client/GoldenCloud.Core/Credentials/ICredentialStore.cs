using System;

namespace GoldenCloud.Core.Credentials;

/// <summary>A username/password pair for one GoldenCloud server.</summary>
public sealed class StoredCredential
{
    public StoredCredential(string username, string password)
    {
        if (string.IsNullOrWhiteSpace(username))
        {
            throw new ArgumentException("Username must not be empty.", nameof(username));
        }

        Username = username;
        Password = password ?? throw new ArgumentNullException(nameof(password));
    }

    public string Username { get; }

    /// <summary>
    /// The password. Held in managed memory only for as long as it takes to hand
    /// it to the credential store or the mount process; never serialised to disk
    /// and never placed on a command line (D-006).
    /// </summary>
    public string Password { get; }

    /// <summary>Never includes the password.</summary>
    public override string ToString() => "StoredCredential(" + Username + ", ********)";
}

/// <summary>
/// Abstraction over the platform credential vault. The Windows implementation
/// lives in GoldenCloud.Tray and P/Invokes CredWriteW/CredReadW/CredDeleteW;
/// tests use <see cref="InMemoryCredentialStore"/>.
/// </summary>
public interface ICredentialStore
{
    /// <summary>Returns the stored credential, or null when there is none.</summary>
    StoredCredential? Read(string target);

    /// <summary>Creates or replaces the stored credential.</summary>
    void Write(string target, StoredCredential credential);

    /// <summary>Removes the stored credential. Returns false when nothing was stored.</summary>
    bool Delete(string target);
}

/// <summary>Well-known credential target names.</summary>
public static class CredentialTargets
{
    /// <summary>Shown in Windows Credential Manager under "Generic Credentials".</summary>
    public const string Default = "GoldenCloud";
}
