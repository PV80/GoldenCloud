using System;
using System.Threading;
using System.Threading.Tasks;
using GoldenCloud.Core.Credentials;
using GoldenCloud.Core.WebDav;

namespace GoldenCloud.Core;

/// <summary>
/// Sign-in / sign-out, sitting between the UI and the two things it needs: a
/// credential vault and a way to check credentials with the server.
///
/// Nothing is stored until the server has accepted the credentials, and nothing
/// is stored at all by a build with no server address baked in (D-007).
/// </summary>
public sealed class AccountService
{
    private readonly ICredentialStore _store;
    private readonly IWebDavProbe _probe;
    private readonly string _serverUrl;
    private readonly string _target;

    public AccountService(
        string serverUrl,
        ICredentialStore store,
        IWebDavProbe probe,
        string target = CredentialTargets.Default)
    {
        _serverUrl = serverUrl ?? string.Empty;
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _probe = probe ?? throw new ArgumentNullException(nameof(probe));
        _target = string.IsNullOrWhiteSpace(target) ? CredentialTargets.Default : target;
    }

    /// <summary>The address this client was built for.</summary>
    public string ServerUrl => _serverUrl;

    public string CredentialTarget => _target;

    /// <summary>True when the build has no real server address (D-007).</summary>
    public bool IsUnconfiguredBuild => ServerUrlValidator.IsUnconfigured(_serverUrl);

    public StoredCredential? Current => _store.Read(_target);

    public bool IsSignedIn => Current is not null;

    /// <summary>
    /// Verifies the credentials against the server and, only on success, writes
    /// them to the credential vault.
    /// </summary>
    public async Task<SignInResult> SignInAsync(
        string username,
        string password,
        bool remember = true,
        CancellationToken cancellationToken = default)
    {
        if (IsUnconfiguredBuild)
        {
            return SignInResult.Fail(
                SignInOutcome.UnconfiguredBuild,
                "This build has no server address. It must be rebuilt with GOLDENCLOUD_SERVER_URL set before it can be used.");
        }

        SignInResult result = await _probe
            .SignInAsync(_serverUrl, username, password, cancellationToken)
            .ConfigureAwait(false);

        if (result.Succeeded && remember)
        {
            _store.Write(_target, new StoredCredential(username.Trim(), password));
        }

        return result;
    }

    /// <summary>Deletes the stored credential. Returns false when there was none.</summary>
    public bool SignOut() => _store.Delete(_target);
}
