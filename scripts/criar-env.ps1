$ErrorActionPreference = "Stop"
function Novo-Token {
  $bytes = New-Object byte[] 24
  [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
  ($bytes | ForEach-Object { $_.ToString("x2") }) -join ""
}
$conteudo = Get-Content ".env.example"
$conteudo = $conteudo -replace "^LEME_TOKEN=.*", ("LEME_TOKEN=" + (Novo-Token))
$conteudo = $conteudo -replace "^LEME_TOKEN_CONSOLE=.*", ("LEME_TOKEN_CONSOLE=" + (Novo-Token))
Set-Content -Path ".env" -Value $conteudo -Encoding ascii
Write-Host "Arquivo .env criado com tokens aleatorios."
