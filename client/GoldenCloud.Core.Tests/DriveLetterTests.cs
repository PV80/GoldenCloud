using System;
using GoldenCloud.Core.Mounting;
using Xunit;

namespace GoldenCloud.Core.Tests;

public class DriveLetterTests
{
    [Theory]
    [InlineData("G", "G:")]
    [InlineData("g", "G:")]
    [InlineData("G:", "G:")]
    [InlineData("g:", "G:")]
    [InlineData(@"G:\", "G:")]
    [InlineData("  z:  ", "Z:")]
    [InlineData("D", "D:")]
    public void Normalises_the_forms_people_actually_type(string input, string expected)
    {
        Assert.True(DriveLetters.TryNormalise(input, out string normalised));
        Assert.Equal(expected, normalised);
        Assert.Equal(expected, DriveLetters.Normalise(input));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("GG")]
    [InlineData("1")]
    [InlineData("::")]
    [InlineData(@"\\server\share")]
    [InlineData("A")] // historic floppy letters
    [InlineData("B")]
    [InlineData("C")] // the system volume
    public void Rejects_unusable_drive_letters(string? input)
    {
        Assert.False(DriveLetters.TryNormalise(input, out string normalised));
        Assert.Equal(string.Empty, normalised);
        Assert.False(DriveLetters.IsValid(input));
        Assert.Throws<ArgumentException>(() => DriveLetters.Normalise(input));
    }

    [Fact]
    public void The_default_is_G()
    {
        Assert.Equal("G:", DriveLetters.Default);
        Assert.True(DriveLetters.IsValid(DriveLetters.Default));
    }

    [Fact]
    public void Root_path_is_what_Directory_Exists_needs()
    {
        Assert.Equal(@"G:\", DriveLetters.ToRootPath("g"));
        Assert.Equal(@"H:\", DriveLetters.ToRootPath("H:"));
    }
}
