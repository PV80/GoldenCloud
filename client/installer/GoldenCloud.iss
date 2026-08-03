; GoldenCloud Windows client installer.
;
; Compiled by ../package.ps1, which passes:
;   /DAppVersion=<version>
;   /DSourceDir=<absolute path to client/build/app>
;   /DThirdPartyDir=<absolute path to client/thirdparty>
;
; It produces exactly Output\GoldenCloudSetup.exe, which CI publishes.
;
; What it installs:
;   * the self-contained tray app (no .NET runtime needed on the PC);
;   * rclone.exe beside it, for the primary mount strategy (D-005);
;   * WinFsp, but only when it is not already present;
;   * an HKCU Run entry so the tray starts with Windows.

#define AppName       "GoldenCloud"
#define AppPublisher  "GoldenCloud"
#define AppExeName    "GoldenCloud.Tray.exe"

#ifndef AppVersion
  #define AppVersion "0.1.0"
#endif

#ifndef SourceDir
  #define SourceDir "..\build\app"
#endif

#ifndef ThirdPartyDir
  #define ThirdPartyDir "..\thirdparty"
#endif

[Setup]
AppId={{EE10D6DB-8BA5-458B-9E75-23BA4D0EFCC6}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
VersionInfoVersion={#AppVersion}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
DisableDirPage=auto
UninstallDisplayName={#AppName}
UninstallDisplayIcon={app}\{#AppExeName}
OutputDir=Output
OutputBaseFilename=GoldenCloudSetup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
; Administrator rights are needed to install WinFsp and to write to Program Files.
PrivilegesRequired=admin
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
MinVersion=10.0
CloseApplications=yes
RestartApplications=no
SetupLogging=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
; The published tray app. Symbols are stripped by build.ps1; excluded again here
; so a hand-run publish cannot leak them into the installer.
Source: "{#SourceDir}\*"; DestDir: "{app}"; Excludes: "*.pdb,*.xml"; \
    Flags: ignoreversion recursesubdirs createallsubdirs

; rclone, the primary mount strategy.
Source: "{#ThirdPartyDir}\rclone.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#ThirdPartyDir}\rclone-LICENSE.txt"; DestDir: "{app}"; \
    Flags: ignoreversion skipifsourcedoesntexist

; The WinFsp redistributable, extracted only when it is actually needed.
Source: "{#ThirdPartyDir}\winfsp.msi"; DestDir: "{tmp}"; \
    Flags: deleteafterinstall; Check: not IsWinFspInstalled

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExeName}"
Name: "{group}\{#AppName} diagnostics"; Filename: "{app}\{#AppExeName}"; Parameters: "--diagnostics"
Name: "{group}\Uninstall {#AppName}"; Filename: "{uninstallexe}"

[Registry]
; "Start with Windows". The tray app's own menu item toggles this same value.
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; \
    ValueType: string; ValueName: "GoldenCloud"; \
    ValueData: """{app}\{#AppExeName}"""; Flags: uninsdeletevalue

[Run]
; WinFsp first: the tray app checks for it on startup and picks its strategy.
Filename: "msiexec.exe"; \
    Parameters: "/i ""{tmp}\winfsp.msi"" /qn /norestart"; \
    StatusMsg: "Installing WinFsp (needed for the drive letter)..."; \
    Check: not IsWinFspInstalled; Flags: waituntilterminated

Filename: "{app}\{#AppExeName}"; Description: "Start {#AppName} now"; \
    Flags: nowait postinstall skipifsilent

[UninstallRun]
; Stop the tray app before removing its files.
Filename: "{sys}\taskkill.exe"; Parameters: "/IM {#AppExeName} /F"; \
    Flags: runhidden skipifdoesntexist; RunOnceId: "StopGoldenCloudTray"

[Code]
function IsWinFspInstalled: Boolean;
begin
  { WinFsp registers under WOW6432Node even on 64-bit Windows, and always
    creates its driver service key. Either is proof enough. }
  Result := RegKeyExists(HKEY_LOCAL_MACHINE, 'SOFTWARE\WOW6432Node\WinFsp') or
            RegKeyExists(HKEY_LOCAL_MACHINE, 'SOFTWARE\WinFsp') or
            RegKeyExists(HKEY_LOCAL_MACHINE, 'SYSTEM\CurrentControlSet\Services\WinFsp');
end;
