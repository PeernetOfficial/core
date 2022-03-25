# System

This submodule defines how files and plugins are managed and updated.

## Package Files

Package files are ZIP files containing a file `info.ini` that describes which actions to take. They provide updates and installation of individual files, plugins, or entire clients.

Actions may specify target files or folders. Such paths may use virtual paths that will be automatically resolved.

```
%plugin% = Plugin folder
%data%   = Data folder
```

## Sample Package File

A sample package file `TextViewer.zip` looks like this:

```
│   info.ini
│
└───TextViewer
        Microsoft.Extensions.DependencyInjection.Abstractions.dll
        Peernet.Browser.Plugins.TextViewer.deps.json
        Peernet.Browser.Plugins.TextViewer.dll
        Peernet.SDK.dll
```

The `info.ini` file:

```ini
[main]
name = "Text Viewer Plugin"
organization = "Peernet s.r.o."
architecture = "windows/amd64"

[action1]
action = extract
source = TextViewer
target = "%plugin%"
```

## Security Implications

The actions currently only allow to extract any arbitrary file to the local disk. Later, the destination directory will be limited to the Peernet installation directory.

Signatures for packages are not implemented yet. They will be added in a subsequent version.

## Target OS

It is up to the client to select and download the appropriate packages.
