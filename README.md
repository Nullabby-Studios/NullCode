# NullCode

A minimal terminal coding agent for the Nullabby model family. Speaks any
OpenAI-compatible chat API (llama.cpp llama-server, vLLM, LM Studio, Ollama
/v1) and uses a one-line glyph directive protocol — plus optional native
OpenAI tool calling for frontier models.

## Build

```powershell
.\build.ps1          # dist\nullcode.exe, dist\NullCode-0.1.0-setup.msi, setup.exe (+ linux/darwin)
```

The `.msi` is a standard per-user Windows installer (WiX v5): it installs
`nullcode.exe` into `%LOCALAPPDATA%\Programs\NullCode` and adds that folder to
your user PATH. Install by double-clicking or `msiexec /i
NullCode-0.1.0-setup.msi`; uninstall from "Installed apps" (note: uninstall
currently leaves the PATH entry behind — harmless, remove it manually if you
care). Requires `dotnet tool install --global wix --version 5.0.2` to build.

`setup.exe` also embeds `nullcode.exe` and installs the same way
(`setup.exe -uninstall` removes it).

## Run

```powershell
.\dist\nullcode.exe
```

- First launch walks you through provider token, URL and model (saved to
  `%APPDATA%\nullcode\config.json`); `/connect` adds another provider.
- `/model` opens a modal with every connected provider's models grouped under
  provider headers, with a search bar. Picking a model from another provider
  switches the endpoint.
- `/` opens the command palette (`/help`, `/functions`, `/tools`, `/init`,
  `/sessions`, `/theme`, …).
- The agent acts through one-line directives (`⟦exec⟧`, `⟦write⟧`,
  `⟦patch⟧`, `⟦done⟧`, …) or native tool calls — switchable with `/tools`.
