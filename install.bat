@echo off
rem Copies the plugin, with the pdx-painter it runs, into Painter's plugin folder
rem of the current user. Run build.bat first.
setlocal
cd /d "%~dp0"

if not exist "plugin\PDX Painter\bin\pdx-painter.exe" (
	echo pdx-painter is not built; run build.bat first.
	exit /b 1
)

for /f "usebackq delims=" %%d in (`powershell -NoProfile -Command "[Environment]::GetFolderPath('MyDocuments')"`) do set DOCUMENTS=%%d

set PLUGINS=%DOCUMENTS%\Adobe\Adobe Substance 3D Painter\python\plugins
set TARGET=%PLUGINS%\PDX Painter

robocopy "plugin\PDX Painter" "%TARGET%" /MIR /XD __pycache__ /NFL /NDL /NJH /NJS /NP
if errorlevel 8 exit /b 1

rem The first version was installed as pdx_painter.
if exist "%PLUGINS%\pdx_painter\__init__.py" rmdir /s /q "%PLUGINS%\pdx_painter"

echo Installed into %TARGET%
echo In Painter, enable it under Python ^> PDX Painter, or restart Painter.
exit /b 0
