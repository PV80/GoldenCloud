using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Threading;
using System.Threading.Tasks;

namespace GoldenCloud.Core.Tests.Fakes;

/// <summary>
/// A stand-in WebDAV server. Records what the client sent and answers with
/// whatever the test asked for. Nothing here opens a socket.
/// </summary>
public sealed class StubHttpMessageHandler : HttpMessageHandler
{
    private readonly Func<HttpRequestMessage, HttpResponseMessage> _responder;

    public StubHttpMessageHandler(Func<HttpRequestMessage, HttpResponseMessage> responder)
    {
        _responder = responder ?? throw new ArgumentNullException(nameof(responder));
    }

    /// <summary>Every request the client made, in order.</summary>
    public List<CapturedRequest> Requests { get; } = new();

    public int CallCount => Requests.Count;

    public CapturedRequest LastRequest =>
        Requests.Count > 0
            ? Requests[Requests.Count - 1]
            : throw new InvalidOperationException("No request was made.");

    /// <summary>A handler that answers every request with one status code.</summary>
    public static StubHttpMessageHandler WithStatus(HttpStatusCode status, string body = "") =>
        new(_ => new HttpResponseMessage(status) { Content = new StringContent(body) });

    /// <summary>A handler that answers 207 for the right credentials and 401 otherwise.</summary>
    public static StubHttpMessageHandler WebDavServer(string expectedUsername, string expectedPassword)
    {
        const string multiStatusBody =
            "<?xml version=\"1.0\" encoding=\"utf-8\"?>" +
            "<D:multistatus xmlns:D=\"DAV:\"><D:response><D:href>/</D:href>" +
            "<D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop>" +
            "<D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>";

        return new StubHttpMessageHandler(request =>
        {
            System.Net.Http.Headers.AuthenticationHeaderValue? auth = request.Headers.Authorization;
            if (auth is null ||
                !string.Equals(auth.Scheme, "Basic", StringComparison.OrdinalIgnoreCase) ||
                auth.Parameter is null)
            {
                return Unauthorized();
            }

            string decoded;
            try
            {
                decoded = System.Text.Encoding.UTF8.GetString(Convert.FromBase64String(auth.Parameter));
            }
            catch (FormatException)
            {
                return Unauthorized();
            }

            int separator = decoded.IndexOf(':');
            if (separator < 0)
            {
                return Unauthorized();
            }

            string user = decoded.Substring(0, separator);
            string pass = decoded.Substring(separator + 1);

            if (user != expectedUsername || pass != expectedPassword)
            {
                return Unauthorized();
            }

            var ok = new HttpResponseMessage((HttpStatusCode)207)
            {
                Content = new StringContent(multiStatusBody, System.Text.Encoding.UTF8, "application/xml"),
            };
            ok.Headers.TryAddWithoutValidation("DAV", "1, 2");
            return ok;
        });

        static HttpResponseMessage Unauthorized()
        {
            var response = new HttpResponseMessage(HttpStatusCode.Unauthorized)
            {
                Content = new StringContent(string.Empty),
            };
            response.Headers.TryAddWithoutValidation("WWW-Authenticate", "Basic realm=\"GoldenCloud\"");
            return response;
        }
    }

    public HttpClient CreateClient() => new(this, disposeHandler: false);

    protected override async Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken)
    {
        string? body = null;
        if (request.Content is not null)
        {
            body = await request.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
        }

        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (KeyValuePair<string, IEnumerable<string>> header in request.Headers)
        {
            headers[header.Key] = string.Join(", ", header.Value);
        }

        Requests.Add(new CapturedRequest(
            request.Method.Method,
            request.RequestUri?.ToString() ?? string.Empty,
            request.Headers.Authorization?.Scheme,
            request.Headers.Authorization?.Parameter,
            headers,
            body));

        HttpResponseMessage response = _responder(request);
        response.RequestMessage = request;
        return response;
    }

    /// <summary>One recorded request. Decodes the Basic credential for assertions.</summary>
    public sealed record CapturedRequest(
        string Method,
        string Url,
        string? AuthScheme,
        string? AuthParameter,
        IReadOnlyDictionary<string, string> Headers,
        string? Body)
    {
        public string? DecodedAuthorization =>
            AuthParameter is null
                ? null
                : System.Text.Encoding.UTF8.GetString(Convert.FromBase64String(AuthParameter));
    }
}
