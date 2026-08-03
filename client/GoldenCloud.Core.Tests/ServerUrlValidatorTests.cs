using GoldenCloud.Core;
using Xunit;

namespace GoldenCloud.Core.Tests;

public class ServerUrlValidatorTests
{
    // ---- trailing-slash and case normalisation ------------------------------

    [Theory]
    [InlineData("https://cloud.example.com", "https://cloud.example.com/")]
    [InlineData("https://cloud.example.com/", "https://cloud.example.com/")]
    [InlineData("https://cloud.example.com///", "https://cloud.example.com/")]
    [InlineData("  https://cloud.example.com  ", "https://cloud.example.com/")]
    [InlineData("HTTPS://CLOUD.EXAMPLE.COM", "https://cloud.example.com/")]
    [InlineData("https://cloud.example.com:443", "https://cloud.example.com/")]
    [InlineData("https://cloud.example.com:8443", "https://cloud.example.com:8443/")]
    [InlineData("https://cloud.example.com/dav", "https://cloud.example.com/dav/")]
    [InlineData("https://cloud.example.com/dav//", "https://cloud.example.com/dav/")]
    public void Normalises_valid_addresses(string input, string expected)
    {
        ServerUrlValidationResult result = ServerUrlValidator.Validate(input);

        Assert.True(result.IsValid, result.Message);
        Assert.Equal(expected, result.NormalisedUrl);
        Assert.NotNull(result.Uri);
    }

    [Fact]
    public void Normalisation_is_idempotent()
    {
        ServerUrlValidationResult once = ServerUrlValidator.Validate("https://cloud.example.com///");
        ServerUrlValidationResult twice = ServerUrlValidator.Validate(once.NormalisedUrl);

        Assert.True(twice.IsValid);
        Assert.Equal(once.NormalisedUrl, twice.NormalisedUrl);
    }

    // ---- plain http is refused unless the host is loopback -------------------

    [Theory]
    [InlineData("http://cloud.example.com/")]
    [InlineData("http://192.168.1.10/")]
    [InlineData("http://8.8.8.8/")]
    [InlineData("http://intranet/")]
    public void Rejects_plain_http_to_a_non_loopback_host(string input)
    {
        ServerUrlValidationResult result = ServerUrlValidator.Validate(input);

        Assert.False(result.IsValid);
        Assert.Equal(ServerUrlError.InsecureTransport, result.Error);
    }

    [Theory]
    [InlineData("http://localhost/", "http://localhost/")]
    [InlineData("http://localhost:8080", "http://localhost:8080/")]
    [InlineData("http://127.0.0.1:8080/", "http://127.0.0.1:8080/")]
    [InlineData("http://127.0.0.1/", "http://127.0.0.1/")]
    public void Allows_plain_http_to_loopback(string input, string expected)
    {
        ServerUrlValidationResult result = ServerUrlValidator.Validate(input);

        Assert.True(result.IsValid, result.Message);
        Assert.Equal(expected, result.NormalisedUrl);
    }

    // ---- malformed input ----------------------------------------------------

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Rejects_empty_input(string? input)
    {
        ServerUrlValidationResult result = ServerUrlValidator.Validate(input);

        Assert.False(result.IsValid);
        Assert.Equal(ServerUrlError.Empty, result.Error);
    }

    [Theory]
    [InlineData("cloud.example.com")]   // no scheme
    [InlineData("//cloud.example.com")] // protocol-relative
    [InlineData("https://")]            // no host
    [InlineData("https://cloud.example.com:notaport/")]
    [InlineData("https://cloud.example.com:99999/")]
    [InlineData("not a url at all")]
    [InlineData("https:///onlyapath")]
    public void Rejects_malformed_addresses(string input)
    {
        ServerUrlValidationResult result = ServerUrlValidator.Validate(input);

        Assert.False(result.IsValid);
        Assert.Null(result.NormalisedUrl);
        Assert.NotEqual(ServerUrlError.None, result.Error);
    }

    [Theory]
    [InlineData("ftp://cloud.example.com/")]
    [InlineData("file://C:/temp/")]
    [InlineData("smb://server/share")]
    public void Rejects_unsupported_schemes(string input)
    {
        ServerUrlValidationResult result = ServerUrlValidator.Validate(input);

        Assert.False(result.IsValid);
    }

    [Fact]
    public void Rejects_addresses_carrying_credentials()
    {
        ServerUrlValidationResult result = ServerUrlValidator.Validate("https://alice:hunter2@cloud.example.com/");

        Assert.False(result.IsValid);
        Assert.Equal(ServerUrlError.ContainsCredentials, result.Error);
    }

    [Theory]
    [InlineData("https://cloud.example.com/?token=abc")]
    [InlineData("https://cloud.example.com/#frag")]
    public void Rejects_query_strings_and_fragments(string input)
    {
        ServerUrlValidationResult result = ServerUrlValidator.Validate(input);

        Assert.False(result.IsValid);
        Assert.Equal(ServerUrlError.ContainsQueryOrFragment, result.Error);
    }

    // ---- the D-007 unconfigured placeholder ---------------------------------

    [Theory]
    [InlineData("https://cloud.example.com")]
    [InlineData("https://cloud.example.com/")]
    [InlineData("HTTPS://Cloud.Example.COM/")]
    [InlineData("")]
    [InlineData(null)]
    [InlineData("nonsense")]
    public void Recognises_the_unconfigured_placeholder(string? input)
    {
        Assert.True(ServerUrlValidator.IsUnconfigured(input));
    }

    [Theory]
    [InlineData("https://cloud.acme.co.uk/")]
    [InlineData("https://drive.goldenivy.example/")]
    public void A_real_address_is_not_the_placeholder(string input)
    {
        Assert.False(ServerUrlValidator.IsUnconfigured(input));
    }
}
