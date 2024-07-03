param (
    [string]$inputFile,
    [string]$gaijiFile,
    [string]$outputFile
)

# 調査文字を読み込む
$gaijiChars = Get-Content -Path $gaijiFile

# 出力ファイルの初期化
Out-File -FilePath $outputFile -Force

# 入力ファイルを1行ずつ読み込み
Get-Content -Path $inputFile | ForEach-Object {
    $line = $_
    foreach ($char in $gaijiChars) {
        if ($line -like "*$char*") {
            # 調査文字と行の内容を出力ファイルに書き込む
            "$char,$line" | Out-File -FilePath $outputFile -Append
            break
        }
    }
}
