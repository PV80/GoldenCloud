using System.Net;
using System.Net.Http;
using System.Threading.Tasks;
using GoldenCloud.Core;
using GoldenCloud.Core.Tests.Fakes;
using GoldenCloud.Core.WebDav;
using Xunit;

namespace GoldenCloud.Core.Tests;

/// <summary>
/// Sign-in against a mocked WebDAV server. Nothing here touches the network.
/// </summary>
public class WebDavSignInTests
{
    private const string ServerUrl = "https://cloud.acme.test/";
    private const string User = "alice";
    private const string Password = "correct horse battery staple";

    [Fact]
    public async Task Sign_in_succeeds_when_the_server_answers_207()
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync(ServerUrl, User, Password);

        Assert.True(result.Succeeded, result.ToString());
        Assert.Equal(SignInOutcome.Success, result.Outcome);
        Assert.Equal(207, result.StatusCode);
    }

    [Fact]
    public async Task Sign_in_fails_when_the_server_answers_401()
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync(ServerUrl, User, "the wrong password");

        Assert.False(result.Succeeded);
        Assert.Equal(SignInOutcome.InvalidCredentials, result.Outcome);
        Assert.Equal(401, result.StatusCode);
    }

    [Fact]
    public async Task Sign_in_uses_PROPFIND_with_Depth_0_and_Basic_auth()
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        await probe.SignInAsync(ServerUrl, User, Password);

        Assert.Equal(1, handler.CallCount);
        StubHttpMessageHandler.CapturedRequest request = handler.LastRequest;

        Assert.Equal("PROPFIND", request.Method);
        Assert.Equal(ServerUrl, request.Url);
        Assert.Equal("Basic", request.AuthScheme);
        Assert.Equal(User + ":" + Password, request.DecodedAuthorization);
        Assert.Equal("0", request.Headers["Depth"]);
        Assert.Contains("DAV:", request.Body ?? string.Empty);
    }

    [Fact]
    public async Task Sign_in_normalises_the_address_before_sending()
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync("HTTPS://Cloud.ACME.test", User, Password);

        Assert.True(result.Succeeded, result.ToString());
        Assert.Equal("https://cloud.acme.test/", handler.LastRequest.Url);
    }

    [Fact]
    public async Task Nothing_is_transmitted_over_plain_http_to_a_remote_host()
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync("http://cloud.acme.test/", User, Password);

        Assert.Equal(SignInOutcome.InsecureTransport, result.Outcome);
        Assert.Equal(0, handler.CallCount);
    }

    [Fact]
    public async Task Nothing_is_transmitted_when_the_address_is_malformed()
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync("cloud.acme.test", User, Password);

        Assert.Equal(SignInOutcome.InvalidServerUrl, result.Outcome);
        Assert.Equal(0, handler.CallCount);
    }

    [Theory]
    [InlineData(HttpStatusCode.MethodNotAllowed, SignInOutcome.NotWebDav)]
    [InlineData(HttpStatusCode.NotImplemented, SignInOutcome.NotWebDav)]
    [InlineData(HttpStatusCode.BadGateway, SignInOutcome.ServerError)]
    [InlineData(HttpStatusCode.ServiceUnavailable, SignInOutcome.ServerError)]
    [InlineData(HttpStatusCode.NotFound, SignInOutcome.ServerError)]
    [InlineData(HttpStatusCode.Forbidden, SignInOutcome.InvalidCredentials)]
    public async Task Maps_server_statuses_to_outcomes(HttpStatusCode status, SignInOutcome expected)
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WithStatus(status);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync(ServerUrl, User, Password);

        Assert.Equal(expected, result.Outcome);
        Assert.Equal((int)status, result.StatusCode);
    }

    [Fact]
    public async Task A_200_answer_also_counts_as_success()
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WithStatus(HttpStatusCode.OK);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync(ServerUrl, User, Password);

        Assert.True(result.Succeeded);
    }

    [Fact]
    public async Task A_transport_failure_is_reported_as_a_network_error()
    {
        using var handler = new StubHttpMessageHandler(_ => throw new HttpRequestException("no such host"));
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync(ServerUrl, User, Password);

        Assert.Equal(SignInOutcome.NetworkError, result.Outcome);
        Assert.Null(result.StatusCode);
    }

    [Theory]
    [InlineData("", "somepassword")]
    [InlineData("   ", "somepassword")]
    [InlineData("alice", "")]
    public async Task Empty_fields_are_refused_without_a_round_trip(string user, string password)
    {
        using StubHttpMessageHandler handler = StubHttpMessageHandler.WebDavServer(User, Password);
        using HttpClient client = handler.CreateClient();
        var probe = new WebDavProbe(client);

        SignInResult result = await probe.SignInAsync(ServerUrl, user, password);

        Assert.Equal(SignInOutcome.InvalidCredentials, result.Outcome);
        Assert.Equal(0, handler.CallCount);
    }
}
