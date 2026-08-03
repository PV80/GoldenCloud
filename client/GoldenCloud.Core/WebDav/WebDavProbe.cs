using System;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace GoldenCloud.Core.WebDav;

/// <summary>Verifies a username/password against a WebDAV endpoint.</summary>
public interface IWebDavProbe
{
    Task<SignInResult> SignInAsync(
        string serverUrl,
        string username,
        string password,
        CancellationToken cancellationToken = default);
}

/// <summary>
/// Issues a <c>PROPFIND</c> with <c>Depth: 0</c> and HTTP Basic auth against the
/// server root. A WebDAV server answers 207 Multi-Status when the credentials
/// are good and 401 when they are not, so one round trip settles it.
///
/// The address is validated first, and nothing is transmitted at all if the
/// address would put the password on the wire in clear text (D-003, D-006).
/// </summary>
public sealed class WebDavProbe : IWebDavProbe
{
    /// <summary>PROPFIND is not one of the constants on <see cref="HttpMethod"/>.</summary>
    public static readonly HttpMethod PropFind = new("PROPFIND");

    private const string PropFindBody =
        "<?xml version=\"1.0\" encoding=\"utf-8\"?>" +
        "<D:propfind xmlns:D=\"DAV:\"><D:prop><D:resourcetype/></D:prop></D:propfind>";

    private readonly HttpClient _http;

    public WebDavProbe(HttpClient http)
    {
        _http = http ?? throw new ArgumentNullException(nameof(http));
    }

    public async Task<SignInResult> SignInAsync(
        string serverUrl,
        string username,
        string password,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(username))
        {
            return SignInResult.Fail(SignInOutcome.InvalidCredentials, "Enter your username.");
        }

        if (string.IsNullOrEmpty(password))
        {
            return SignInResult.Fail(SignInOutcome.InvalidCredentials, "Enter your password.");
        }

        ServerUrlValidationResult validation = ServerUrlValidator.Validate(serverUrl);
        if (!validation.IsValid || validation.Uri is null)
        {
            SignInOutcome outcome = validation.Error == ServerUrlError.InsecureTransport
                ? SignInOutcome.InsecureTransport
                : SignInOutcome.InvalidServerUrl;
            return SignInResult.Fail(outcome, validation.Message);
        }

        using var request = new HttpRequestMessage(PropFind, validation.Uri);
        request.Headers.Add("Depth", "0");
        request.Headers.Authorization = new AuthenticationHeaderValue(
            "Basic",
            Convert.ToBase64String(Encoding.UTF8.GetBytes(username + ":" + password)));
        request.Content = new StringContent(PropFindBody, new UTF8Encoding(false), "application/xml");

        HttpResponseMessage response;
        try
        {
            response = await _http
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            // HttpClient surfaces its own timeout as a cancellation too, so tell
            // the two apart by asking the caller's token.
            return cancellationToken.IsCancellationRequested
                ? SignInResult.Fail(SignInOutcome.Cancelled, "Sign-in was cancelled.")
                : SignInResult.Fail(SignInOutcome.Timeout, "The server did not answer in time.");
        }
        catch (HttpRequestException ex)
        {
            return SignInResult.Fail(
                SignInOutcome.NetworkError,
                "Could not reach " + validation.NormalisedUrl + ": " + ex.Message);
        }

        using (response)
        {
            int status = (int)response.StatusCode;

            // 207 Multi-Status is the correct WebDAV answer. Some proxies collapse
            // it to 200 for a Depth: 0 request, which is still proof of auth.
            if (status == 207 || status == 200)
            {
                return SignInResult.Ok(status);
            }

            if (status == 401)
            {
                return SignInResult.Fail(
                    SignInOutcome.InvalidCredentials,
                    "That username and password were not accepted.",
                    status);
            }

            if (status == 403)
            {
                return SignInResult.Fail(
                    SignInOutcome.InvalidCredentials,
                    "The server refused access for this account.",
                    status);
            }

            if (status == 405 || status == 501)
            {
                return SignInResult.Fail(
                    SignInOutcome.NotWebDav,
                    "The address answered, but it is not a WebDAV server.",
                    status);
            }

            return SignInResult.Fail(
                SignInOutcome.ServerError,
                "The server answered with HTTP " + status + ".",
                status);
        }
    }
}
