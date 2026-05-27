@echo off
setlocal

echo ============================================
echo  OpenContext Windows UI Collector Installer
echo ============================================
echo.

:: Check Python
python --version >nul 2>&1
if errorlevel 1 (
    echo [ERROR] Python not found.
    echo Please install Python 3.10+ from https://python.org
    echo Make sure to check "Add Python to PATH" during install.
    pause
    exit /b 1
)

for /f "tokens=*" %%v in ('python --version 2^>^&1') do echo Found: %%v

:: Install dependencies
echo.
echo Installing Python dependencies...
pip install -r requirements.txt
if errorlevel 1 (
    echo [ERROR] pip install failed.
    pause
    exit /b 1
)

echo.
echo ============================================
echo  Installation complete!
echo ============================================
echo.
echo Usage:
echo   python collector.py                 -- run in foreground
echo   python collector.py --debug         -- verbose output
echo   python collector.py --dry-run       -- print events, don't push
echo   pythonw collector.py                -- run silently in background
echo.
echo Configuration: %%USERPROFILE%%\.opencontext\windows-collector.yaml
echo.
echo Make sure contextd is running at http://localhost:6060
echo.
pause
