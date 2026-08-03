using System;

namespace GoldenCloud.Core.Mounting;

/// <summary>Validation and normalisation of the target drive letter.</summary>
public static class DriveLetters
{
    /// <summary>The default drive letter staff see in File Explorer.</summary>
    public const string Default = "G:";

    /// <summary>
    /// Accepts "g", "G", "g:", "G:", "G:\" and returns "G:".
    /// A, B and C are refused: A/B are the historic floppy letters that some
    /// software still special-cases, and C is the system volume.
    /// </summary>
    public static bool TryNormalise(string? candidate, out string normalised)
    {
        normalised = string.Empty;

        if (string.IsNullOrWhiteSpace(candidate))
        {
            return false;
        }

        string text = candidate.Trim().TrimEnd('\\', '/');
        if (text.Length == 2 && text[1] == ':')
        {
            text = text.Substring(0, 1);
        }

        if (text.Length != 1)
        {
            return false;
        }

        char letter = char.ToUpperInvariant(text[0]);
        if (letter < 'A' || letter > 'Z')
        {
            return false;
        }

        if (letter == 'A' || letter == 'B' || letter == 'C')
        {
            return false;
        }

        normalised = letter + ":";
        return true;
    }

    public static bool IsValid(string? candidate) => TryNormalise(candidate, out _);

    /// <summary>Throws <see cref="ArgumentException"/> rather than returning false.</summary>
    public static string Normalise(string? candidate)
    {
        if (!TryNormalise(candidate, out string normalised))
        {
            throw new ArgumentException(
                "'" + (candidate ?? "<null>") + "' is not a usable drive letter (D: to Z:).",
                nameof(candidate));
        }

        return normalised;
    }

    /// <summary>"G:" becomes "G:\", which is what Directory.Exists needs.</summary>
    public static string ToRootPath(string driveLetter) => Normalise(driveLetter) + "\\";
}
