using System;
using System.Collections.Generic;
using System.IO;
using System.Runtime.Versioning;
using System.Text;
using GoldenCloud.Core.Mounting;

namespace GoldenCloud.Tray;

/// <summary>Which mount strategy the user has asked for.</summary>
internal enum StrategyPreference
{
    /// <summary>rclone if WinFsp is present, otherwise net use.</summary>
    Automatic = 0,
    ForceRclone = 1,
    ForceNetUse = 2,
}

/// <summary>
/// The handful of non-secret preferences, kept in a two-line key=value file
/// under %LOCALAPPDATA%. Deliberately not JSON and deliberately never holding
/// anything sensitive: credentials live in Credential Manager only (D-006).
/// </summary>
[SupportedOSPlatform("windows")]
internal sealed class AppSettings
{
    private const string DriveKey = "drive";
    private const string StrategyKey = "strategy";
    private const string LastUserKey = "lastuser";

    public string DriveLetter { get; set; } = DriveLetters.Default;

    public StrategyPreference Strategy { get; set; } = StrategyPreference.Automatic;

    /// <summary>Pre-fills the sign-in box. Never the password.</summary>
    public string LastUsername { get; set; } = string.Empty;

    public static AppSettings Load()
    {
        var settings = new AppSettings();

        string path;
        try
        {
            path = WindowsPaths.SettingsFile;
        }
        catch (IOException)
        {
            return settings;
        }

        if (!File.Exists(path))
        {
            return settings;
        }

        try
        {
            foreach (string rawLine in File.ReadAllLines(path))
            {
                string line = rawLine.Trim();
                if (line.Length == 0 || line[0] == '#' || line[0] == ';')
                {
                    continue;
                }

                int separator = line.IndexOf('=');
                if (separator <= 0)
                {
                    continue;
                }

                string key = line.Substring(0, separator).Trim().ToLowerInvariant();
                string value = line.Substring(separator + 1).Trim();

                switch (key)
                {
                    case DriveKey:
                        if (DriveLetters.TryNormalise(value, out string drive))
                        {
                            settings.DriveLetter = drive;
                        }

                        break;

                    case StrategyKey:
                        if (Enum.TryParse(value, ignoreCase: true, out StrategyPreference strategy))
                        {
                            settings.Strategy = strategy;
                        }

                        break;

                    case LastUserKey:
                        settings.LastUsername = value;
                        break;
                }
            }
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // A corrupt or unreadable settings file falls back to defaults.
        }

        return settings;
    }

    public void Save()
    {
        var lines = new List<string>
        {
            "# GoldenCloud client settings. No credentials are stored here.",
            DriveKey + "=" + DriveLetter,
            StrategyKey + "=" + Strategy,
            LastUserKey + "=" + LastUsername,
        };

        try
        {
            string path = WindowsPaths.SettingsFile;
            string temporary = path + ".tmp";
            File.WriteAllLines(temporary, lines, new UTF8Encoding(false));
            File.Move(temporary, path, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Preferences are a convenience; failing to persist them is not fatal.
        }
    }
}
