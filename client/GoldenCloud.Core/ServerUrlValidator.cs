using System;
using System.Net;

namespace GoldenCloud.Core;

/// <summary>Why a candidate server address was rejected.</summary>
public enum ServerUrlError
{
    None = 0,
    Empty,
    Malformed,
    UnsupportedScheme,
    /// <summary>Plain <c>http://</c> to a host that is not loopback.</summary>
    InsecureTransport,
    ContainsCredentials,
    ContainsQueryOrFragment,
}

/// <summary>Outcome of validating and normalising a server address.</summary>
public sealed class ServerUrlValidationResult
{
    private ServerUrlValidationResult(bool isValid, Uri? uri, string? normalised, ServerUrlError error, string? message)
    {
        IsValid = isValid;
        Uri = uri;
        NormalisedUrl = normalised;
        Error = error;
        Message = message ?? string.Empty;
    }

    public bool IsValid { get; }

    /// <summary>The normalised absolute URI. Non-null exactly when <see cref="IsValid"/>.</summary>
    public Uri? Uri { get; }

    /// <summary>The normalised URL string, always ending in a single '/'. Non-null exactly when <see cref="IsValid"/>.</summary>
    public string? NormalisedUrl { get; }

    public ServerUrlError Error { get; }

    public string Message { get; }

    internal static ServerUrlValidationResult Ok(Uri uri) =>
        new(true, uri, uri.ToString(), ServerUrlError.None, null);

    internal static ServerUrlValidationResult Fail(ServerUrlError error, string message) =>
        new(false, null, null, error, message);
}

/// <summary>
/// Validates and normalises the server address the client talks to.
/// Pure logic, no I/O, so it is fully unit-testable off Windows.
/// </summary>
public static class ServerUrlValidator
{
    /// <summary>
    /// The placeholder address used by a build that has not had
    /// <c>GOLDENCLOUD_SERVER_URL</c> supplied (D-007).
    /// </summary>
    public const string UnconfiguredUrl = "https://cloud.example.com";

    /// <summary>
    /// True when the supplied address is the unconfigured placeholder. Compared
    /// after normalisation so trailing slashes and casing do not matter.
    /// </summary>
    public static bool IsUnconfigured(string? candidate)
    {
        if (string.IsNullOrWhiteSpace(candidate))
        {
            return true;
        }

        ServerUrlValidationResult placeholder = Validate(UnconfiguredUrl);
        ServerUrlValidationResult actual = Validate(candidate);
        if (!actual.IsValid || !placeholder.IsValid)
        {
            return !actual.IsValid;
        }

        return string.Equals(actual.NormalisedUrl, placeholder.NormalisedUrl, StringComparison.OrdinalIgnoreCase);
    }

    public static ServerUrlValidationResult Validate(string? candidate)
    {
        if (string.IsNullOrWhiteSpace(candidate))
        {
            return ServerUrlValidationResult.Fail(ServerUrlError.Empty, "Server address is empty.");
        }

        string trimmed = candidate.Trim();

        if (!Uri.TryCreate(trimmed, UriKind.Absolute, out Uri? uri) || uri is null)
        {
            return ServerUrlValidationResult.Fail(
                ServerUrlError.Malformed,
                "'" + trimmed + "' is not a valid absolute URL.");
        }

        bool isHttps = string.Equals(uri.Scheme, "https", StringComparison.OrdinalIgnoreCase);
        bool isHttp = string.Equals(uri.Scheme, "http", StringComparison.OrdinalIgnoreCase);
        if (!isHttps && !isHttp)
        {
            return ServerUrlValidationResult.Fail(
                ServerUrlError.UnsupportedScheme,
                "Only https:// addresses are supported (http:// is allowed for localhost only).");
        }

        if (string.IsNullOrEmpty(uri.Host) || Uri.CheckHostName(uri.Host) == UriHostNameType.Unknown)
        {
            return ServerUrlValidationResult.Fail(
                ServerUrlError.Malformed,
                "'" + trimmed + "' does not contain a usable host name.");
        }

        if (!string.IsNullOrEmpty(uri.UserInfo))
        {
            return ServerUrlValidationResult.Fail(
                ServerUrlError.ContainsCredentials,
                "The server address must not contain a username or password.");
        }

        if (!string.IsNullOrEmpty(uri.Query) || !string.IsNullOrEmpty(uri.Fragment))
        {
            return ServerUrlValidationResult.Fail(
                ServerUrlError.ContainsQueryOrFragment,
                "The server address must not contain a query string or fragment.");
        }

        if (isHttp && !IsLoopback(uri))
        {
            return ServerUrlValidationResult.Fail(
                ServerUrlError.InsecureTransport,
                "Refusing to send credentials over plain http:// to '" + uri.Host + "'. Use https://.");
        }

        string host = StripBrackets(uri.Host).ToLowerInvariant();
        if (host.IndexOf(':') >= 0)
        {
            // UriBuilder.Host expects an IPv6 literal to keep its brackets.
            host = "[" + host + "]";
        }

        var builder = new UriBuilder
        {
            Scheme = uri.Scheme.ToLowerInvariant(),
            Host = host,
            Port = uri.IsDefaultPort ? -1 : uri.Port,
            Path = NormalisePath(uri.AbsolutePath),
        };

        return ServerUrlValidationResult.Ok(builder.Uri);
    }

    /// <summary>True for localhost, 127.0.0.0/8 and ::1.</summary>
    public static bool IsLoopback(Uri uri)
    {
        if (uri is null)
        {
            return false;
        }

        if (uri.IsLoopback)
        {
            return true;
        }

        string host = StripBrackets(uri.Host);
        if (string.Equals(host, "localhost", StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        return IPAddress.TryParse(host, out IPAddress? address) && address is not null && IPAddress.IsLoopback(address);
    }

    private static string StripBrackets(string host) =>
        host.Length > 1 && host[0] == '[' && host[host.Length - 1] == ']'
            ? host.Substring(1, host.Length - 2)
            : host;

    /// <summary>Collapses repeated trailing slashes to exactly one.</summary>
    private static string NormalisePath(string path)
    {
        if (string.IsNullOrEmpty(path))
        {
            return "/";
        }

        string p = path.Trim();
        while (p.Length > 1 && p.EndsWith("/", StringComparison.Ordinal))
        {
            p = p.Substring(0, p.Length - 1);
        }

        if (!p.StartsWith("/", StringComparison.Ordinal))
        {
            p = "/" + p;
        }

        return p == "/" ? "/" : p + "/";
    }
}
