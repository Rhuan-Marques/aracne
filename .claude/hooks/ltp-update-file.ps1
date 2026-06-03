$inputJson = [Console]::In.ReadToEnd()
if ([string]::IsNullOrWhiteSpace($inputJson)) { exit 0 }
try { $event = $inputJson | ConvertFrom-Json } catch { exit 0 }
$toolInput = $event.tool_input
if ($null -eq $toolInput) { exit 0 }
$paths = New-Object System.Collections.Generic.List[string]
foreach ($name in @("file_path", "filePath", "path")) {
  if ($toolInput.PSObject.Properties.Name -contains $name) {
    $value = $toolInput.$name
    if (-not [string]::IsNullOrWhiteSpace($value)) { $paths.Add($value) }
  }
}
if ($toolInput.PSObject.Properties.Name -contains "edits") {
  foreach ($edit in $toolInput.edits) {
    foreach ($name in @("file_path", "filePath", "path")) {
      if ($edit.PSObject.Properties.Name -contains $name) {
        $value = $edit.$name
        if (-not [string]::IsNullOrWhiteSpace($value)) { $paths.Add($value) }
      }
    }
  }
}
foreach ($file in ($paths | Select-Object -Unique)) {
  $out = & ltp update-file $file 2>&1 | Out-String
  if ($LASTEXITCODE -ne 0) {
    Write-Output "llm-topology update-file failed for ${file}:`n$out"
    continue
  }
  if ($out -notmatch "Warning number\s+0") {
    Write-Output "llm-topology warnings for ${file}:`n$out"
  }
}
