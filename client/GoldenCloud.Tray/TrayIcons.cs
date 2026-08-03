using System;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Drawing.Text;
using System.Runtime.Versioning;

namespace GoldenCloud.Tray;

/// <summary>
/// Draws the tray icon at runtime rather than shipping a .ico, so there is no
/// binary blob in the repository and the icon can reflect connection state.
/// </summary>
[SupportedOSPlatform("windows")]
internal static class TrayIcons
{
    private static readonly Color Gold = Color.FromArgb(0xE0, 0xA8, 0x1E);
    private static readonly Color Grey = Color.FromArgb(0x8A, 0x8A, 0x8A);
    private static readonly Color Amber = Color.FromArgb(0xE8, 0x7A, 0x1E);
    private static readonly Color Red = Color.FromArgb(0xC8, 0x30, 0x30);

    private static Icon? _connected;
    private static Icon? _disconnected;
    private static Icon? _working;
    private static Icon? _failed;

    public static Icon ForState(MountState state) => state switch
    {
        MountState.Connected => _connected ??= Build(Gold, null),
        MountState.Connecting => _working ??= Build(Gold, Amber),
        MountState.Retrying => _working ??= Build(Gold, Amber),
        MountState.Failed => _failed ??= Build(Grey, Red),
        _ => _disconnected ??= Build(Grey, null),
    };

    private static Icon Build(Color body, Color? badge)
    {
        const int size = 32;

        using var bitmap = new Bitmap(size, size);
        using (Graphics graphics = Graphics.FromImage(bitmap))
        {
            graphics.SmoothingMode = SmoothingMode.AntiAlias;
            graphics.TextRenderingHint = TextRenderingHint.AntiAliasGridFit;
            graphics.Clear(Color.Transparent);

            using (var brush = new SolidBrush(body))
            {
                graphics.FillEllipse(brush, 1, 1, size - 3, size - 3);
            }

            using (var pen = new Pen(Color.FromArgb(0x40, 0, 0, 0), 1f))
            {
                graphics.DrawEllipse(pen, 1, 1, size - 3, size - 3);
            }

            using (var font = new Font(FontFamily.GenericSansSerif, 15f, FontStyle.Bold, GraphicsUnit.Pixel))
            using (var textBrush = new SolidBrush(Color.FromArgb(0x20, 0x20, 0x20)))
            using (var format = new StringFormat
            {
                Alignment = StringAlignment.Center,
                LineAlignment = StringAlignment.Center,
            })
            {
                graphics.DrawString("G", font, textBrush, new RectangleF(0, 0, size, size), format);
            }

            if (badge.HasValue)
            {
                using var badgeBrush = new SolidBrush(badge.Value);
                graphics.FillEllipse(badgeBrush, size - 13, size - 13, 12, 12);
            }
        }

        IntPtr handle = bitmap.GetHicon();
        try
        {
            // Clone so the Icon survives DestroyIcon on the temporary handle.
            using var temporary = Icon.FromHandle(handle);
            return (Icon)temporary.Clone();
        }
        finally
        {
            NativeMethods.DestroyIcon(handle);
        }
    }
}

[SupportedOSPlatform("windows")]
internal static class NativeMethods
{
    [System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true)]
    [return: System.Runtime.InteropServices.MarshalAs(System.Runtime.InteropServices.UnmanagedType.Bool)]
    internal static extern bool DestroyIcon(IntPtr handle);
}
