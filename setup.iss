#define AppName "PanTunnel Desktop"
#define AppVersion "1.1.0"
#define AppPublisher "JhopanStore"
#define AppExeName "PanTunnel-Desktop.exe"
#define AppCoreName "PanTunnel-Core-windows-amd64.exe"

[Setup]
AppId={{8F1C2A64-7D31-4E19-9E2B-2C6A5B7D4F10}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
DefaultDirName={autopf}\PanTunnel Desktop
DefaultGroupName=PanTunnel Desktop
DisableProgramGroupPage=yes
OutputDir=output
OutputBaseFilename=PanTunnel-Desktop-Setup-v{#AppVersion}
SetupIconFile=assets\app.ico
Compression=lzma2
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
UninstallDisplayIcon={app}\{#AppExeName}
WizardStyle=modern

[Files]
Source: "bin\{#AppExeName}"; DestDir: "{app}"; Flags: ignoreversion
; Core sing-box. Ambil dari release core-v* JPS Tunnel, atau rename sing-box.exe.
Source: "bin\{#AppCoreName}"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist
Source: "assets\app.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\PanTunnel Desktop"; Filename: "{app}\{#AppExeName}"; IconFilename: "{app}\{#AppExeName}"
Name: "{autodesktop}\PanTunnel Desktop"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon; IconFilename: "{app}\{#AppExeName}"

[Tasks]
Name: "desktopicon"; Description: "Buat shortcut Desktop"; GroupDescription: "Shortcut:"

[UninstallDelete]
Type: files; Name: "{app}\*.json"
