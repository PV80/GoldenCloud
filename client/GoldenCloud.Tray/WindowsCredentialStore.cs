using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Runtime.Versioning;
using System.Text;
using GoldenCloud.Core.Credentials;

namespace GoldenCloud.Tray;

/// <summary>
/// Windows Credential Manager, reached through advapi32 (D-006).
///
/// The password is written as a generic credential blob, which Windows encrypts
/// at rest under the signed-in user's DPAPI key. It is never written to a file,
/// never placed in the registry and never put on a command line.
/// </summary>
[SupportedOSPlatform("windows")]
internal sealed class WindowsCredentialStore : ICredentialStore
{
    private const uint CRED_TYPE_GENERIC = 1;

    /// <summary>Visible to this user on this machine only; not roamed.</summary>
    private const uint CRED_PERSIST_LOCAL_MACHINE = 2;

    private const int ERROR_NOT_FOUND = 1168;

    public StoredCredential? Read(string target)
    {
        if (string.IsNullOrEmpty(target))
        {
            return null;
        }

        if (!CredReadW(target, CRED_TYPE_GENERIC, 0, out IntPtr handle))
        {
            int error = Marshal.GetLastWin32Error();
            if (error == ERROR_NOT_FOUND)
            {
                return null;
            }

            throw new Win32Exception(error, "Could not read the GoldenCloud credential.");
        }

        try
        {
            CREDENTIAL credential = Marshal.PtrToStructure<CREDENTIAL>(handle);

            string userName = credential.UserName == IntPtr.Zero
                ? string.Empty
                : Marshal.PtrToStringUni(credential.UserName) ?? string.Empty;

            string password = string.Empty;
            if (credential.CredentialBlob != IntPtr.Zero && credential.CredentialBlobSize > 0)
            {
                // The blob is UTF-16, and its size is in bytes.
                password = Marshal.PtrToStringUni(
                    credential.CredentialBlob,
                    (int)(credential.CredentialBlobSize / 2)) ?? string.Empty;
            }

            return string.IsNullOrEmpty(userName) ? null : new StoredCredential(userName, password);
        }
        finally
        {
            CredFree(handle);
        }
    }

    public void Write(string target, StoredCredential credential)
    {
        if (string.IsNullOrEmpty(target))
        {
            throw new ArgumentException("Target must not be empty.", nameof(target));
        }

        if (credential is null)
        {
            throw new ArgumentNullException(nameof(credential));
        }

        byte[] blob = Encoding.Unicode.GetBytes(credential.Password);

        IntPtr targetPtr = IntPtr.Zero;
        IntPtr userPtr = IntPtr.Zero;
        IntPtr commentPtr = IntPtr.Zero;
        IntPtr blobPtr = IntPtr.Zero;

        try
        {
            targetPtr = Marshal.StringToCoTaskMemUni(target);
            userPtr = Marshal.StringToCoTaskMemUni(credential.Username);
            commentPtr = Marshal.StringToCoTaskMemUni("GoldenCloud private drive");

            blobPtr = Marshal.AllocCoTaskMem(Math.Max(blob.Length, 1));
            if (blob.Length > 0)
            {
                Marshal.Copy(blob, 0, blobPtr, blob.Length);
            }

            var native = new CREDENTIAL
            {
                Flags = 0,
                Type = CRED_TYPE_GENERIC,
                TargetName = targetPtr,
                Comment = commentPtr,
                LastWritten = default,
                CredentialBlobSize = (uint)blob.Length,
                CredentialBlob = blobPtr,
                Persist = CRED_PERSIST_LOCAL_MACHINE,
                AttributeCount = 0,
                Attributes = IntPtr.Zero,
                TargetAlias = IntPtr.Zero,
                UserName = userPtr,
            };

            if (!CredWriteW(ref native, 0))
            {
                throw new Win32Exception(
                    Marshal.GetLastWin32Error(),
                    "Could not save the credential to Windows Credential Manager.");
            }
        }
        finally
        {
            // Overwrite the managed copy of the password bytes before releasing.
            Array.Clear(blob, 0, blob.Length);

            if (blobPtr != IntPtr.Zero)
            {
                Marshal.FreeCoTaskMem(blobPtr);
            }

            if (commentPtr != IntPtr.Zero)
            {
                Marshal.FreeCoTaskMem(commentPtr);
            }

            if (userPtr != IntPtr.Zero)
            {
                Marshal.FreeCoTaskMem(userPtr);
            }

            if (targetPtr != IntPtr.Zero)
            {
                Marshal.FreeCoTaskMem(targetPtr);
            }
        }
    }

    public bool Delete(string target)
    {
        if (string.IsNullOrEmpty(target))
        {
            return false;
        }

        if (CredDeleteW(target, CRED_TYPE_GENERIC, 0))
        {
            return true;
        }

        int error = Marshal.GetLastWin32Error();
        if (error == ERROR_NOT_FOUND)
        {
            return false;
        }

        throw new Win32Exception(error, "Could not delete the GoldenCloud credential.");
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct CREDENTIAL
    {
        public uint Flags;
        public uint Type;
        public IntPtr TargetName;
        public IntPtr Comment;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public uint CredentialBlobSize;
        public IntPtr CredentialBlob;
        public uint Persist;
        public uint AttributeCount;
        public IntPtr Attributes;
        public IntPtr TargetAlias;
        public IntPtr UserName;
    }

    [DllImport("advapi32.dll", EntryPoint = "CredReadW", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredReadW(string targetName, uint type, uint reservedFlag, out IntPtr credentialPtr);

    [DllImport("advapi32.dll", EntryPoint = "CredWriteW", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredWriteW(ref CREDENTIAL userCredential, uint flags);

    [DllImport("advapi32.dll", EntryPoint = "CredDeleteW", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredDeleteW(string targetName, uint type, uint flags);

    [DllImport("advapi32.dll", EntryPoint = "CredFree")]
    private static extern void CredFree(IntPtr buffer);
}
