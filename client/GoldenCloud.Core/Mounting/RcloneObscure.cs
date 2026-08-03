using System;
using System.Security.Cryptography;
using System.Text;

namespace GoldenCloud.Core.Mounting;

/// <summary>Produces the "obscured" form of a password that rclone expects.</summary>
public interface IPasswordObscurer
{
    string Obscure(string plaintext);
}

/// <summary>
/// Managed re-implementation of rclone's <c>obscure</c> encoding.
///
/// rclone refuses a WebDAV password that is not obscured, so the value handed to
/// <c>RCLONE_WEBDAV_PASS</c> has to be in this form. The encoding is AES-CTR
/// under a fixed key that ships inside rclone itself, with a random 16-byte IV
/// prepended, the whole thing then base64url-encoded without padding.
///
/// It is deliberately NOT a security boundary — rclone's own documentation says
/// so — it exists only to stop a password being readable at a glance. The real
/// protection is that the value never touches disk and never enters an argument
/// vector (D-006).
///
/// The tray app prefers to shell out to the bundled <c>rclone.exe obscure</c>
/// and only falls back to this implementation if that fails, so a drift in
/// rclone's encoding cannot silently break sign-in.
/// </summary>
public static class RcloneObscure
{
    private static readonly byte[] Key =
    {
        0x9c, 0x93, 0x5b, 0x48, 0x73, 0x0a, 0x55, 0x4d,
        0x6b, 0xfd, 0x7c, 0x63, 0xc8, 0x86, 0xa9, 0x2b,
        0xd3, 0x90, 0x19, 0x8e, 0xb8, 0x12, 0x8a, 0xfb,
        0xf4, 0xde, 0x16, 0x2b, 0x8b, 0x95, 0xf6, 0x38,
    };

    private const int BlockSize = 16;

    public static string Obscure(string plaintext)
    {
        if (plaintext is null)
        {
            throw new ArgumentNullException(nameof(plaintext));
        }

        var iv = new byte[BlockSize];
        RandomNumberGenerator.Fill(iv);
        return Obscure(plaintext, iv);
    }

    /// <summary>Deterministic overload; the IV is supplied by the caller. For tests.</summary>
    public static string Obscure(string plaintext, byte[] iv)
    {
        if (plaintext is null)
        {
            throw new ArgumentNullException(nameof(plaintext));
        }

        if (iv is null || iv.Length != BlockSize)
        {
            throw new ArgumentException("IV must be exactly 16 bytes.", nameof(iv));
        }

        byte[] body = Encoding.UTF8.GetBytes(plaintext);
        byte[] cipher = CounterModeXor(iv, body);

        var output = new byte[BlockSize + cipher.Length];
        Buffer.BlockCopy(iv, 0, output, 0, BlockSize);
        Buffer.BlockCopy(cipher, 0, output, BlockSize, cipher.Length);

        return ToBase64Url(output);
    }

    /// <summary>The inverse of <see cref="Obscure(string)"/>.</summary>
    public static string Reveal(string obscured)
    {
        if (obscured is null)
        {
            throw new ArgumentNullException(nameof(obscured));
        }

        byte[] raw = FromBase64Url(obscured);
        if (raw.Length < BlockSize)
        {
            throw new FormatException("Obscured value is too short to contain an IV.");
        }

        var iv = new byte[BlockSize];
        Buffer.BlockCopy(raw, 0, iv, 0, BlockSize);

        var cipher = new byte[raw.Length - BlockSize];
        Buffer.BlockCopy(raw, BlockSize, cipher, 0, cipher.Length);

        return Encoding.UTF8.GetString(CounterModeXor(iv, cipher));
    }

    /// <summary>
    /// AES-CTR. .NET has no CTR mode, so the counter blocks are encrypted with
    /// AES-ECB and XORed with the data, which is exactly what CTR is.
    /// </summary>
    private static byte[] CounterModeXor(byte[] iv, byte[] data)
    {
#pragma warning disable CA5358 // ECB here is the CTR keystream primitive, not a mode of operation on the data.
        using Aes aes = Aes.Create();
        aes.Key = Key;
        aes.Mode = CipherMode.ECB;
        aes.Padding = PaddingMode.None;
#pragma warning restore CA5358

        using ICryptoTransform encryptor = aes.CreateEncryptor();

        var counter = new byte[BlockSize];
        Buffer.BlockCopy(iv, 0, counter, 0, BlockSize);

        var keystream = new byte[BlockSize];
        var result = new byte[data.Length];

        for (int offset = 0; offset < data.Length; offset += BlockSize)
        {
            encryptor.TransformBlock(counter, 0, BlockSize, keystream, 0);

            int take = Math.Min(BlockSize, data.Length - offset);
            for (int i = 0; i < take; i++)
            {
                result[offset + i] = (byte)(data[offset + i] ^ keystream[i]);
            }

            IncrementBigEndian(counter);
        }

        return result;
    }

    /// <summary>Go's crypto/cipher CTR treats the IV as a big-endian counter.</summary>
    private static void IncrementBigEndian(byte[] counter)
    {
        for (int i = counter.Length - 1; i >= 0; i--)
        {
            counter[i]++;
            if (counter[i] != 0)
            {
                return;
            }
        }
    }

    /// <summary>base64.RawURLEncoding: URL alphabet, no '=' padding.</summary>
    public static string ToBase64Url(byte[] value)
    {
        if (value is null)
        {
            throw new ArgumentNullException(nameof(value));
        }

        return Convert.ToBase64String(value)
            .TrimEnd('=')
            .Replace('+', '-')
            .Replace('/', '_');
    }

    public static byte[] FromBase64Url(string value)
    {
        if (value is null)
        {
            throw new ArgumentNullException(nameof(value));
        }

        string padded = value.Trim().Replace('-', '+').Replace('_', '/');
        switch (padded.Length % 4)
        {
            case 2:
                padded += "==";
                break;
            case 3:
                padded += "=";
                break;
            case 1:
                throw new FormatException("Not a valid base64url string.");
        }

        return Convert.FromBase64String(padded);
    }
}

/// <summary>The pure-managed <see cref="IPasswordObscurer"/>.</summary>
public sealed class ManagedPasswordObscurer : IPasswordObscurer
{
    public string Obscure(string plaintext) => RcloneObscure.Obscure(plaintext);
}
