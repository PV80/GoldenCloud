using System;
using System.Linq;
using GoldenCloud.Core.Mounting;
using Xunit;

namespace GoldenCloud.Core.Tests;

/// <summary>
/// The managed re-implementation of rclone's obscure encoding. These tests prove
/// the encoding is self-consistent, non-deterministic and URL-safe. They cannot
/// prove byte-compatibility with rclone itself — see client/README.md — which is
/// why the tray app shells out to rclone.exe first and only falls back to this.
/// </summary>
public class RcloneObscureTests
{
    [Theory]
    [InlineData("")]
    [InlineData("a")]
    [InlineData("hunter2")]
    [InlineData("correct horse battery staple")]
    [InlineData("exactly-sixteen!")]
    [InlineData("a password that is comfortably longer than one aes block")]
    [InlineData("pa$$ /w:th \"quotes\" & <angles> | pipes ^carets")]
    [InlineData("naïve café — ünïcödé ✓")]
    public void Obscure_then_reveal_returns_the_original(string plaintext)
    {
        string obscured = RcloneObscure.Obscure(plaintext);

        Assert.Equal(plaintext, RcloneObscure.Reveal(obscured));
    }

    [Fact]
    public void Obscuring_the_same_password_twice_gives_different_output()
    {
        string first = RcloneObscure.Obscure("hunter2");
        string second = RcloneObscure.Obscure("hunter2");

        Assert.NotEqual(first, second);
        Assert.Equal("hunter2", RcloneObscure.Reveal(first));
        Assert.Equal("hunter2", RcloneObscure.Reveal(second));
    }

    [Fact]
    public void A_supplied_iv_makes_the_encoding_deterministic()
    {
        var iv = new byte[16];
        for (int i = 0; i < iv.Length; i++)
        {
            iv[i] = (byte)i;
        }

        string first = RcloneObscure.Obscure("hunter2", iv);
        string second = RcloneObscure.Obscure("hunter2", iv);

        Assert.Equal(first, second);
        Assert.Equal("hunter2", RcloneObscure.Reveal(first));
    }

    [Fact]
    public void Output_is_url_safe_base64_without_padding()
    {
        string obscured = RcloneObscure.Obscure("a password long enough to need two blocks of keystream");

        Assert.DoesNotContain("=", obscured);
        Assert.DoesNotContain("+", obscured);
        Assert.DoesNotContain("/", obscured);
        Assert.All(obscured, c =>
            Assert.True(
                char.IsLetterOrDigit(c) || c == '-' || c == '_',
                "unexpected character '" + c + "' in obscured output"));
    }

    [Fact]
    public void The_ciphertext_does_not_contain_the_plaintext()
    {
        const string password = "SuperSecretValue";

        string obscured = RcloneObscure.Obscure(password);

        Assert.DoesNotContain(password, obscured, StringComparison.Ordinal);
    }

    [Fact]
    public void Output_is_the_iv_plus_the_ciphertext()
    {
        const string password = "hunter2";

        string obscured = RcloneObscure.Obscure(password);
        byte[] raw = RcloneObscure.FromBase64Url(obscured);

        // 16-byte IV, then one ciphertext byte per plaintext byte (CTR is a
        // stream cipher, so there is no padding).
        Assert.Equal(16 + System.Text.Encoding.UTF8.GetByteCount(password), raw.Length);
    }

    [Fact]
    public void Base64_url_round_trips_arbitrary_bytes()
    {
        var bytes = Enumerable.Range(0, 256).Select(i => (byte)i).ToArray();

        Assert.Equal(bytes, RcloneObscure.FromBase64Url(RcloneObscure.ToBase64Url(bytes)));
    }

    [Fact]
    public void Revealing_something_too_short_is_rejected()
    {
        Assert.Throws<FormatException>(() => RcloneObscure.Reveal(RcloneObscure.ToBase64Url(new byte[4])));
    }

    [Fact]
    public void A_wrong_sized_iv_is_rejected()
    {
        Assert.Throws<ArgumentException>(() => RcloneObscure.Obscure("hunter2", new byte[8]));
    }

    [Fact]
    public void The_managed_obscurer_implements_the_interface()
    {
        IPasswordObscurer obscurer = new ManagedPasswordObscurer();

        Assert.Equal("hunter2", RcloneObscure.Reveal(obscurer.Obscure("hunter2")));
    }
}
