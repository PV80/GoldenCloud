namespace GoldenCloud.Core.Mounting;

/// <summary>Turns a <see cref="MountRequest"/> into a child-process invocation.</summary>
public interface IMountCommandBuilder
{
    MountStrategy Strategy { get; }

    /// <summary>
    /// Builds the mount command.
    /// </summary>
    /// <param name="request">The mount parameters.</param>
    /// <param name="secret">
    /// For <see cref="MountStrategy.Rclone"/> this is the rclone-obscured password
    /// and is placed in the environment block. For <see cref="MountStrategy.NetUse"/>
    /// it is the plain password and is placed on standard input.
    /// In neither case does it reach the argument vector.
    /// </param>
    MountCommand BuildMount(MountRequest request, string secret);

    /// <summary>
    /// The command that tears the mount down, or null when unmounting is done by
    /// terminating the mount process instead (rclone).
    /// </summary>
    MountCommand? BuildUnmount(MountRequest request);
}
