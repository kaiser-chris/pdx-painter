@echo off
rem Tests and builds pdx-painter into the plugin, where the plugin runs it from.
setlocal
cd /d "%~dp0"

set CGO_ENABLED=0

go test ./... || exit /b 1
go build -trimpath -ldflags "-s -w" -o "plugin\PDX Painter\bin\pdx-painter.exe" ./cmd/pdx-painter || exit /b 1

echo Built plugin\PDX Painter\bin\pdx-painter.exe
