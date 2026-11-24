; Inno Setup script for the ESI Bridge Windows installer.
;
; Built by scripts/package-windows.ps1, which compiles the binaries first and
; passes these defines on the ISCC command line:
;   /DAppVersion=1.2.3        version shown in Apps & Features (digits and dots)
;   /DBuildDir=<path>         folder holding esi-proxy.exe and esi-proxy-cli.exe
;   /DOutputDir=<path>        where the setup .exe is written
;
; It installs per user (no admin prompt) into %LOCALAPPDATA%\Programs, which
; matches the app registering its eveauth-proxy:// handler under HKCU.

#ifndef AppVersion
  #error AppVersion must be defined, e.g. ISCC /DAppVersion=1.0.0
#endif
#ifndef BuildDir
  #error BuildDir must be defined
#endif
#ifndef OutputDir
  #define OutputDir "."
#endif

#define AppName "ESI Bridge"
#define AppExe "esi-proxy.exe"
#define RepoRoot AddBackslash(SourcePath) + "..\.."

[Setup]
; The app's auto-updater finds the install through this AppId's uninstall
; key (innoUninstallKey in internal/update/apply_windows.go); keep them in step.
AppId={{2642D8DA-C1C0-42A3-92C2-037FF57CE3DE}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher=BOS Development
AppPublisherURL=https://github.com/BOS-Development/esi-proxy
AppSupportURL=https://github.com/BOS-Development/esi-proxy/issues
VersionInfoVersion={#AppVersion}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
LicenseFile={#RepoRoot}\LICENSE
OutputDir={#OutputDir}
OutputBaseFilename=esi-proxy-{#AppVersion}-windows-x64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
SetupIconFile={#RepoRoot}\internal\assets\bridge.ico
UninstallDisplayIcon={app}\{#AppExe}
UninstallDisplayName={#AppName}
; The app lives in the tray, so an upgrade has to stop it first.
CloseApplications=force
RestartApplications=no

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "startup"; Description: "Start {#AppName} when I sign in to Windows"; GroupDescription: "Startup:"; Flags: unchecked

[Files]
Source: "{#BuildDir}\esi-proxy.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BuildDir}\esi-proxy-cli.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#RepoRoot}\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion

[InstallDelete]
; Shortcuts from before the rename to ESI Bridge; upgrades would otherwise
; leave them behind next to the new ones.
Type: files; Name: "{autoprograms}\ESI Proxy.lnk"
Type: files; Name: "{autodesktop}\ESI Proxy.lnk"
Type: files; Name: "{userstartup}\ESI Proxy.lnk"

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#AppExe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon
Name: "{userstartup}\{#AppName}"; Filename: "{app}\{#AppExe}"; Tasks: startup

[Registry]
; The eveauth-proxy:// SSO callback handler. The app also (re)writes this on
; every start (internal/urlscheme), but registering it here means login works
; right after install and the key is removed on uninstall.
Root: HKCU; Subkey: "Software\Classes\eveauth-proxy"; ValueType: string; ValueName: ""; ValueData: "URL:ESI Bridge Auth Callback"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\eveauth-proxy"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""
Root: HKCU; Subkey: "Software\Classes\eveauth-proxy\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\{#AppExe}"" ""%1"""

[Run]
Filename: "{app}\{#AppExe}"; Description: "{cm:LaunchProgram,{#AppName}}"; Flags: nowait postinstall skipifsilent
; The in-app updater runs this installer with /SILENT /RELAUNCH=1, after the
; old version has quit; start the new version once the files are in place.
Filename: "{app}\{#AppExe}"; Flags: nowait; Check: ShouldRelaunch

[Code]
function ShouldRelaunch: Boolean;
begin
  Result := WizardSilent and (ExpandConstant('{param:RELAUNCH|0}') = '1');
end;

[UninstallRun]
; Stop the running tray app so its files can be removed. User data in
; %APPDATA%\esi-proxy (accounts, tokens) is intentionally left in place.
Filename: "{sys}\taskkill.exe"; Parameters: "/F /T /IM {#AppExe}"; Flags: runhidden; RunOnceId: "StopESIProxy"
