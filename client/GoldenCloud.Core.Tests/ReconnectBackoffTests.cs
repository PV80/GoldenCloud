using System;
using System.Collections.Generic;
using System.Linq;
using GoldenCloud.Core.Reconnect;
using Xunit;

namespace GoldenCloud.Core.Tests;

/// <summary>
/// The auto-reconnect schedule. A deterministic jitter source stands in for the
/// RNG so the schedule can be asserted exactly, with no sleeping.
/// </summary>
public class ReconnectBackoffTests
{
    /// <summary>0.5 is the midpoint of [0,1], so the jitter factor is exactly 1.0.</summary>
    private static Func<double> NoJitter => () => 0.5;

    private static Func<double> Sequence(params double[] samples)
    {
        int index = 0;
        return () => samples[index++ % samples.Length];
    }

    [Fact]
    public void Doubles_from_two_seconds_and_caps_at_five_minutes()
    {
        var backoff = new ReconnectBackoff(BackoffOptions.Default, NoJitter);

        var actual = new List<double>();
        for (int i = 0; i < 12; i++)
        {
            actual.Add(Math.Round(backoff.NextDelay().TotalSeconds, 3));
        }

        Assert.Equal(
            new double[] { 2, 4, 8, 16, 32, 64, 128, 256, 300, 300, 300, 300 },
            actual);
    }

    [Fact]
    public void The_preview_matches_the_live_schedule()
    {
        IReadOnlyList<TimeSpan> preview = ReconnectBackoff.Preview(BackoffOptions.Default, 10);
        var backoff = new ReconnectBackoff(BackoffOptions.Default, NoJitter);

        foreach (TimeSpan expected in preview)
        {
            Assert.Equal(expected.TotalSeconds, backoff.NextDelay().TotalSeconds, 3);
        }
    }

    [Fact]
    public void Attempt_counter_tracks_consecutive_failures()
    {
        var backoff = new ReconnectBackoff(BackoffOptions.Default, NoJitter);

        Assert.Equal(0, backoff.Attempt);
        backoff.NextDelay();
        backoff.NextDelay();
        Assert.Equal(2, backoff.Attempt);
    }

    [Fact]
    public void A_success_resets_the_schedule_to_the_start()
    {
        var backoff = new ReconnectBackoff(BackoffOptions.Default, NoJitter);

        backoff.NextDelay(); // 2s
        backoff.NextDelay(); // 4s
        Assert.Equal(8, backoff.NextDelay().TotalSeconds, 3);

        backoff.Reset();

        Assert.Equal(0, backoff.Attempt);
        Assert.Equal(2, backoff.NextDelay().TotalSeconds, 3);
        Assert.Equal(4, backoff.NextDelay().TotalSeconds, 3);
    }

    [Fact]
    public void Jitter_moves_the_delay_by_at_most_the_configured_fraction()
    {
        var options = new BackoffOptions { JitterFraction = 0.2 };

        // sample 0.0 is the bottom of the band, 1.0 the top.
        var low = new ReconnectBackoff(options, Sequence(0.0));
        var high = new ReconnectBackoff(options, Sequence(1.0));

        Assert.Equal(2.0 * 0.8, low.NextDelay().TotalSeconds, 3);
        Assert.Equal(2.0 * 1.2, high.NextDelay().TotalSeconds, 3);

        Assert.Equal(4.0 * 0.8, low.NextDelay().TotalSeconds, 3);
        Assert.Equal(4.0 * 1.2, high.NextDelay().TotalSeconds, 3);
    }

    [Fact]
    public void Jitter_never_pushes_a_delay_past_the_cap()
    {
        var options = new BackoffOptions { JitterFraction = 0.5 };
        var backoff = new ReconnectBackoff(options, Sequence(1.0));

        for (int i = 0; i < 20; i++)
        {
            TimeSpan delay = backoff.NextDelay();
            Assert.True(delay <= options.MaxDelay, "delay " + delay + " exceeded the cap");
            Assert.True(delay >= TimeSpan.Zero);
        }

        // At the ceiling, the top of the jitter band is the ceiling itself.
        Assert.Equal(options.MaxDelay.TotalSeconds, backoff.NextDelay().TotalSeconds, 3);
    }

    [Fact]
    public void Zero_jitter_produces_the_bare_exponential_schedule()
    {
        var options = new BackoffOptions { JitterFraction = 0.0 };
        var backoff = new ReconnectBackoff(options, Sequence(0.0, 1.0, 0.3));

        Assert.Equal(2, backoff.NextDelay().TotalSeconds, 3);
        Assert.Equal(4, backoff.NextDelay().TotalSeconds, 3);
        Assert.Equal(8, backoff.NextDelay().TotalSeconds, 3);
    }

    [Fact]
    public void Options_are_honoured()
    {
        var options = new BackoffOptions
        {
            InitialDelay = TimeSpan.FromSeconds(1),
            Multiplier = 3.0,
            MaxDelay = TimeSpan.FromSeconds(20),
            JitterFraction = 0.0,
        };

        var schedule = ReconnectBackoff.Preview(options, 5)
            .Select(d => Math.Round(d.TotalSeconds, 3))
            .ToArray();

        Assert.Equal(new double[] { 1, 3, 9, 20, 20 }, schedule);
    }

    [Fact]
    public void A_huge_attempt_number_does_not_overflow_into_a_negative_or_infinite_delay()
    {
        TimeSpan delay = ReconnectBackoff.BaseDelayForAttempt(BackoffOptions.Default, 10_000);

        Assert.Equal(BackoffOptions.Default.MaxDelay, delay);
    }

    [Fact]
    public void Attempt_numbers_below_one_are_treated_as_the_first_attempt()
    {
        Assert.Equal(
            TimeSpan.FromSeconds(2),
            ReconnectBackoff.BaseDelayForAttempt(BackoffOptions.Default, 0));
        Assert.Equal(
            TimeSpan.FromSeconds(2),
            ReconnectBackoff.BaseDelayForAttempt(BackoffOptions.Default, -5));
    }

    [Fact]
    public void The_default_jitter_source_stays_inside_the_band()
    {
        var backoff = new ReconnectBackoff();

        for (int i = 0; i < 200; i++)
        {
            TimeSpan delay = backoff.NextDelay();
            Assert.True(delay > TimeSpan.Zero);
            Assert.True(delay <= BackoffOptions.Default.MaxDelay);
            backoff.Reset();
        }
    }
}
