using System;
using System.Collections.Generic;

namespace GoldenCloud.Core.Reconnect;

/// <summary>Tuning for the auto-reconnect loop.</summary>
public sealed class BackoffOptions
{
    /// <summary>Delay before the first retry.</summary>
    public TimeSpan InitialDelay { get; init; } = TimeSpan.FromSeconds(2);

    /// <summary>Growth factor per consecutive failure.</summary>
    public double Multiplier { get; init; } = 2.0;

    /// <summary>Hard ceiling, jitter included.</summary>
    public TimeSpan MaxDelay { get; init; } = TimeSpan.FromMinutes(5);

    /// <summary>
    /// Fractional jitter, so a room full of PCs that lost the link at the same
    /// moment does not retry in lockstep. 0.2 means +/-20%.
    /// </summary>
    public double JitterFraction { get; init; } = 0.2;

    public static BackoffOptions Default { get; } = new();
}

/// <summary>
/// Exponential backoff with jitter and an explicit reset, extracted from the
/// mount supervisor so the schedule can be asserted on without waiting for
/// real time to pass.
/// </summary>
public sealed class ReconnectBackoff
{
    private readonly BackoffOptions _options;
    private readonly Func<double> _jitterSource;
    private int _attempt;

    /// <param name="options">Null uses <see cref="BackoffOptions.Default"/>.</param>
    /// <param name="jitterSource">
    /// Returns a sample in [0, 1]. Null uses a shared thread-safe RNG.
    /// </param>
    public ReconnectBackoff(BackoffOptions? options = null, Func<double>? jitterSource = null)
    {
        _options = options ?? BackoffOptions.Default;
        _jitterSource = jitterSource ?? (() => Random.Shared.NextDouble());
    }

    /// <summary>How many consecutive failures have been recorded.</summary>
    public int Attempt => _attempt;

    /// <summary>
    /// Records another consecutive failure and returns how long to wait before
    /// the next attempt.
    /// </summary>
    public TimeSpan NextDelay()
    {
        _attempt++;
        TimeSpan baseDelay = BaseDelayForAttempt(_options, _attempt);
        return ApplyJitter(baseDelay, _options.JitterFraction, _jitterSource(), _options.MaxDelay);
    }

    /// <summary>Called after a successful mount: the next failure starts over at the initial delay.</summary>
    public void Reset() => _attempt = 0;

    /// <summary>
    /// The un-jittered delay for the n'th consecutive failure, 1-based:
    /// 2s, 4s, 8s, ... capped at <see cref="BackoffOptions.MaxDelay"/>.
    /// </summary>
    public static TimeSpan BaseDelayForAttempt(BackoffOptions options, int attempt)
    {
        if (options is null)
        {
            throw new ArgumentNullException(nameof(options));
        }

        if (attempt < 1)
        {
            attempt = 1;
        }

        double maxSeconds = options.MaxDelay.TotalSeconds;
        double seconds = options.InitialDelay.TotalSeconds * Math.Pow(options.Multiplier, attempt - 1);

        if (double.IsNaN(seconds) || double.IsInfinity(seconds) || seconds > maxSeconds)
        {
            seconds = maxSeconds;
        }

        if (seconds < 0)
        {
            seconds = 0;
        }

        return TimeSpan.FromSeconds(seconds);
    }

    /// <summary>
    /// Scales <paramref name="delay"/> by 1 +/- <paramref name="fraction"/>,
    /// picked by <paramref name="sample"/> in [0, 1], then clamps to
    /// <paramref name="maxDelay"/> so the ceiling really is a ceiling.
    /// </summary>
    public static TimeSpan ApplyJitter(TimeSpan delay, double fraction, double sample, TimeSpan maxDelay)
    {
        if (fraction <= 0)
        {
            return delay > maxDelay ? maxDelay : delay;
        }

        double clamped = Math.Clamp(sample, 0.0, 1.0);
        double factor = 1.0 + (((clamped * 2.0) - 1.0) * fraction);
        double seconds = delay.TotalSeconds * factor;

        if (seconds < 0)
        {
            seconds = 0;
        }

        if (seconds > maxDelay.TotalSeconds)
        {
            seconds = maxDelay.TotalSeconds;
        }

        return TimeSpan.FromSeconds(seconds);
    }

    /// <summary>The first <paramref name="count"/> un-jittered delays. Diagnostics and tests.</summary>
    public static IReadOnlyList<TimeSpan> Preview(BackoffOptions options, int count)
    {
        var schedule = new List<TimeSpan>(Math.Max(0, count));
        for (int attempt = 1; attempt <= count; attempt++)
        {
            schedule.Add(BaseDelayForAttempt(options, attempt));
        }

        return schedule;
    }
}
