using System;
using System.Collections.Generic;

namespace GoldenCloud.Core.Credentials;

/// <summary>
/// Process-lifetime credential store. Used by the unit tests and by the
/// <c>--dry-run</c> diagnostic mode; never used to persist anything.
/// </summary>
public sealed class InMemoryCredentialStore : ICredentialStore
{
    private readonly Dictionary<string, StoredCredential> _entries =
        new(StringComparer.OrdinalIgnoreCase);

    public int WriteCount { get; private set; }

    public int DeleteCount { get; private set; }

    public int Count => _entries.Count;

    public StoredCredential? Read(string target)
    {
        if (string.IsNullOrEmpty(target))
        {
            return null;
        }

        return _entries.TryGetValue(target, out StoredCredential? credential) ? credential : null;
    }

    public void Write(string target, StoredCredential credential)
    {
        if (string.IsNullOrEmpty(target))
        {
            throw new ArgumentException("Target must not be empty.", nameof(target));
        }

        if (credential is null)
        {
            throw new ArgumentNullException(nameof(credential));
        }

        _entries[target] = credential;
        WriteCount++;
    }

    public bool Delete(string target)
    {
        if (string.IsNullOrEmpty(target))
        {
            return false;
        }

        bool removed = _entries.Remove(target);
        if (removed)
        {
            DeleteCount++;
        }

        return removed;
    }
}
