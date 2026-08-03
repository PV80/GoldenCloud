namespace GoldenCloud.Core.WebDav;

/// <summary>What happened when the client tried to authenticate.</summary>
public enum SignInOutcome
{
    Success = 0,

    /// <summary>The server answered 401 (or 403): wrong username or password.</summary>
    InvalidCredentials,

    /// <summary>The address failed validation before anything was sent.</summary>
    InvalidServerUrl,

    /// <summary>Plain http:// to a non-loopback host. Nothing was sent.</summary>
    InsecureTransport,

    /// <summary>This build has no server address baked in (D-007). Nothing was sent.</summary>
    UnconfiguredBuild,

    /// <summary>The endpoint answered, but does not speak WebDAV.</summary>
    NotWebDav,

    /// <summary>The server answered with an unexpected status.</summary>
    ServerError,

    /// <summary>DNS, TLS or socket failure.</summary>
    NetworkError,

    /// <summary>The request exceeded the client timeout.</summary>
    Timeout,

    /// <summary>The caller cancelled.</summary>
    Cancelled,
}

/// <summary>Result of a sign-in attempt. Never carries the password.</summary>
public sealed class SignInResult
{
    private SignInResult(SignInOutcome outcome, string message, int? statusCode)
    {
        Outcome = outcome;
        Message = message;
        StatusCode = statusCode;
    }

    public SignInOutcome Outcome { get; }

    public string Message { get; }

    /// <summary>The HTTP status code, when the server actually answered.</summary>
    public int? StatusCode { get; }

    public bool Succeeded => Outcome == SignInOutcome.Success;

    public static SignInResult Ok(int statusCode) =>
        new(SignInOutcome.Success, "Signed in.", statusCode);

    public static SignInResult Fail(SignInOutcome outcome, string message, int? statusCode = null) =>
        new(outcome, message, statusCode);

    public override string ToString() =>
        StatusCode.HasValue
            ? Outcome + " (HTTP " + StatusCode.Value + "): " + Message
            : Outcome + ": " + Message;
}
