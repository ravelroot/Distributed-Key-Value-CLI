@echo off
setlocal
cd /d "%~dp0"

where docker >nul 2>nul
if %errorlevel%==0 (
  docker compose version >nul 2>nul
  if not errorlevel 1 goto docker
)
where go >nul 2>nul
if %errorlevel%==0 goto golang
echo Instale o Docker Desktop ou o Go 1.24+ para rodar o Leme.
pause
exit /b 1

:docker
if not exist .env powershell -NoProfile -ExecutionPolicy Bypass -File scripts\criar-env.ps1
docker compose up -d --build
if errorlevel 1 (pause & exit /b 1)
for /f "usebackq tokens=1,* delims==" %%a in (".env") do set "%%a=%%b"
if "%LEME_PORTA_CONSOLE%"=="" set LEME_PORTA_CONSOLE=8080
echo.
echo Cluster com 3 nos no ar.
echo Console: http://127.0.0.1:%LEME_PORTA_CONSOLE%/?token=%LEME_TOKEN_CONSOLE%
echo Modo interativo: docker compose exec console leme --nos no1:7400
start "" "http://127.0.0.1:%LEME_PORTA_CONSOLE%/?token=%LEME_TOKEN_CONSOLE%"
pause
exit /b 0

:golang
if not exist bin mkdir bin
go build -o bin\leme.exe .\cmd\leme || (pause & exit /b 1)
go build -o bin\lemed.exe .\cmd\lemed || (pause & exit /b 1)
start "" "http://127.0.0.1:8080/"
bin\leme.exe dev --console 127.0.0.1:8080
