using System.Threading.Tasks;
using GoldenCloud.Core;
using GoldenCloud.Core.Credentials;
using GoldenCloud.Core.Tests.Fakes;
using GoldenCloud.Core.WebDav;
using Xunit;

namespace GoldenCloud.Core.Tests;

/// <summary>
/// Credential round-trip through the store abstraction, and the rule that
/// nothing is written until the server has said yes (D-006, D-007).
/// </summary>
public class CredentialStorageTests
{
    private const string ServerUrl = "https://cloud.acme.test/";
    private const string User = "alice";
    private const string Password = "correct horse battery staple";

    [Fact]
    public void Store_round_trips_a_credential()
    {
        var store = new InMemoryCredentialStore();

        store.Write(CredentialTargets.Default, new StoredCredential(User, Password));
        StoredCredential? read = store.Read(CredentialTargets.Default);

        Assert.NotNull(read);
        Assert.Equal(User, read!.Username);
        Assert.Equal(Password, read.Password);
    }

    [Fact]
    public void Store_overwrites_rather_than_duplicating()
    {
        var store = new InMemoryCredentialStore();

        store.Write(CredentialTargets.Default, new StoredCredential(User, "old"));
        store.Write(CredentialTargets.Default, new StoredCredential(User, "new"));

        Assert.Equal(1, store.Count);
        Assert.Equal("new", store.Read(CredentialTargets.Default)!.Password);
    }

    [Fact]
    public void Deleting_a_credential_that_is_not_there_reports_false()
    {
        var store = new InMemoryCredentialStore();

        Assert.False(store.Delete(CredentialTargets.Default));
        Assert.Null(store.Read(CredentialTargets.Default));
    }

    [Fact]
    public void A_credential_never_prints_its_password()
    {
        var credential = new StoredCredential(User, Password);

        Assert.DoesNotContain(Password, credential.ToString());
        Assert.Contains(User, credential.ToString());
    }

    [Fact]
    public async Task Successful_sign_in_stores_the_credential()
    {
        var store = new InMemoryCredentialStore();
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        var service = new AccountService(ServerUrl, store, new WebDavProbe(handler.CreateClient()));

        SignInResult result = await service.SignInAsync(User, Password);

        Assert.True(result.Succeeded, result.ToString());
        Assert.True(service.IsSignedIn);
        Assert.Equal(1, store.WriteCount);
        Assert.Equal(User, service.Current!.Username);
        Assert.Equal(Password, service.Current.Password);
    }

    [Fact]
    public async Task Failed_sign_in_stores_nothing()
    {
        var store = new InMemoryCredentialStore();
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        var service = new AccountService(ServerUrl, store, new WebDavProbe(handler.CreateClient()));

        SignInResult result = await service.SignInAsync(User, "wrong");

        Assert.False(result.Succeeded);
        Assert.False(service.IsSignedIn);
        Assert.Equal(0, store.WriteCount);
        Assert.Equal(0, store.Count);
    }

    [Fact]
    public async Task Sign_in_can_verify_without_remembering()
    {
        var store = new InMemoryCredentialStore();
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        var service = new AccountService(ServerUrl, store, new WebDavProbe(handler.CreateClient()));

        SignInResult result = await service.SignInAsync(User, Password, remember: false);

        Assert.True(result.Succeeded);
        Assert.False(service.IsSignedIn);
        Assert.Equal(0, store.WriteCount);
    }

    [Fact]
    public async Task Sign_out_deletes_the_stored_credential()
    {
        var store = new InMemoryCredentialStore();
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        var service = new AccountService(ServerUrl, store, new WebDavProbe(handler.CreateClient()));

        await service.SignInAsync(User, Password);
        Assert.True(service.IsSignedIn);

        bool deleted = service.SignOut();

        Assert.True(deleted);
        Assert.False(service.IsSignedIn);
        Assert.Null(store.Read(CredentialTargets.Default));
        Assert.Equal(0, store.Count);
        Assert.Equal(1, store.DeleteCount);
    }

    [Fact]
    public async Task Sign_out_when_not_signed_in_is_harmless()
    {
        var store = new InMemoryCredentialStore();
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        var service = new AccountService(ServerUrl, store, new WebDavProbe(handler.CreateClient()));

        Assert.False(service.SignOut());
        Assert.False(service.IsSignedIn);

        await Task.CompletedTask;
    }

    // ---- D-007: an unconfigured build refuses to save anything ---------------

    [Fact]
    public async Task An_unconfigured_build_refuses_to_sign_in_or_store()
    {
        var store = new InMemoryCredentialStore();
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        var service = new AccountService(
            ServerUrlValidator.UnconfiguredUrl,
            store,
            new WebDavProbe(handler.CreateClient()));

        Assert.True(service.IsUnconfiguredBuild);

        SignInResult result = await service.SignInAsync(User, Password);

        Assert.Equal(SignInOutcome.UnconfiguredBuild, result.Outcome);
        Assert.Equal(0, handler.CallCount);
        Assert.Equal(0, store.WriteCount);
        Assert.False(service.IsSignedIn);
    }

    [Fact]
    public void A_configured_build_is_not_flagged_as_unconfigured()
    {
        var store = new InMemoryCredentialStore();
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        var service = new AccountService(ServerUrl, store, new WebDavProbe(handler.CreateClient()));

        Assert.False(service.IsUnconfiguredBuild);
    }

    [Fact]
    public async Task Two_accounts_do_not_collide_when_targets_differ()
    {
        var store = new InMemoryCredentialStore();
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        var primary = new AccountService(ServerUrl, store, new WebDavProbe(handler.CreateClient()));
        var secondary = new AccountService(ServerUrl, store, new WebDavProbe(handler.CreateClient()), "GoldenCloud.Secondary");

        await primary.SignInAsync(User, Password);

        Assert.True(primary.IsSignedIn);
        Assert.False(secondary.IsSignedIn);

        primary.SignOut();

        Assert.Equal(0, store.Count);
    }
}
