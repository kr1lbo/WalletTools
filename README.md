# WalletTools

WalletTools - консольный инструмент для генерации красивых EVM-кошельков (vanity addresses), а также для шифрования и дешифрования приватных ключей в формате Ethereum keystore.

Проект умеет:

- генерировать EVM-адреса из случайных приватных ключей и искать совпадения по паттернам;
- генерировать BIP-39 мнемоники с опциональной passphrase и деривировать несколько Ethereum-адресов;
- шифровать raw private keys в keystore-файлы;
- дешифровывать keystore-файлы обратно в raw private keys;
- скрывать секреты в консольном выводе при включенной настройке `hide_secrets_in_console`.

## Важная идея безопасности

Для мнемоник можно использовать BIP-39 passphrase. В интерфейсе это удобно воспринимать как "13-е слово", но технически это отдельная passphrase, которая участвует в получении seed. Без нее та же мнемоника даст другие адреса.

Для приватных ключей используется Ethereum keystore: приватный ключ шифруется паролем и сохраняется в JSON-файл. Без пароля такой файл нельзя использовать для получения приватного ключа.

## Установка

### Готовая программа для Windows x64

[Скачать последний релиз](https://github.com/kr1lbo/WalletTools/releases/latest) → **wallettools-windows-amd64.exe** в разделе Assets.

Сохраните EXE в отдельную папку с правом записи и запустите двойным щелчком. Go и дополнительные библиотеки устанавливать не нужно. Также доступен ZIP с программой, README и лицензией.

При первом запуске рядом с EXE создаются `configs/`, `inputs/encrypt/privates.txt`, `inputs/decrypt/` и `logs/`. Стартовый паттерн ищет префикс `beef` и останавливается после совпадения; измените `configs/patterns.yaml` под свои задачи. Ctrl+C останавливает генерацию и возвращает в меню, Enter в меню завершает программу.

При обновлении заменяйте только EXE: существующие конфиги и данные не перезаписываются. Все относительные пути ниже отсчитываются от папки EXE, независимо от папки запуска. Для другой папки данных:

```powershell
.\wallettools-windows-amd64.exe --data-dir "D:\WalletToolsData"
.\wallettools-windows-amd64.exe --version
Get-FileHash .\wallettools-windows-amd64.exe -Algorithm SHA256
```

Сравните хеш с `checksums.txt` из того же релиза.

### Требования

- Для сборки из исходников: Go 1.24.0 или выше
- Windows, Linux или macOS

### Сборка

```bash
git clone https://github.com/kr1lbo/WalletTools.git
cd WalletTools
go mod download
go build -o wallettools.exe ./cmd/wallettools
```

Если Go ругается на VCS status из-за прав владельца репозитория, можно собрать без VCS stamping:

```bash
go build -buildvcs=false -o wallettools.exe ./cmd/wallettools
```

## Конфигурация

### `configs/app.yaml`

```yaml
# Reserved setting; the current CLI is in English
language: "ru"

# Logger level: debug | info | warn | error
log_level: "info"

# Hide private keys, mnemonics and passwords in console logs
hide_secrets_in_console: true

# How many logical processors to use for generation.
# 0 or no value - use all available ones.
cores: 0
```

`hide_secrets_in_console: true` маскирует секреты только в консоли. Файловые результаты в `logs/` намеренно содержат найденные приватные ключи, мнемоники или расшифрованные ключи, если выбран соответствующий режим.

Старый ключ `hide_secrets` также поддерживается для совместимости, но новый ключ - `hide_secrets_in_console`.

Если оба ключа скрытия отсутствуют, маскирование включено. `language` пока зарезервирован: меню и сообщения операций сейчас на английском. `log_level` действует и при запуске, и внутри операций. `cores: 0` использует все логические процессоры; значение выше доступного автоматически ограничивается.

### `configs/patterns.yaml`

```yaml
symbols: "A B C D E F 0 1 2 3 4 5 6 7 8 9"
case_sensitive: false

symmetric:
  - prefix: "XX"
    suffix: "YY"
    final: true

specific:
  - prefix: "beef"
    suffix: ""
    final: false
  - prefix: "0000"
    suffix: ""
    final: false
  - prefix: "0000"
    suffix: "0000"
    final: false

edges:
  minCount: 6
  side: "any" # any | prefix | suffix
  final: false

regexp:
  - pattern: "(?i)^0x[a-f]{4}"
    final: false
  - pattern: "(?i)face.{0,30}beef"
    final: true
```

`specific`, `symmetric` и `edges` проверяются по телу адреса без префикса `0x`. `regexp` проверяется по полному адресу с `0x`.

Это расширенный пример, а не конфиг первого запуска. Порядок проверки: `symmetric` → `specific` → `edges` → `regexp`; сохраняется первое совпадение. Поэтому более общий паттерн выше может перекрывать более узкий ниже. `final: true` останавливает генерацию после сохранения совпадения; уже найденные другими потоками результаты также могут попасть в файл.

`case_sensitive: true` проверяет регистр checksum-адреса. `symbols` сохранён для совместимости и должен быть непустым, но не ограничивает генерируемые адреса или значения `X`/`Y`.

## Использование

Запуск:

```bash
./wallettools.exe
```

Меню:

```text
WalletTools - Vanity generator
1) Generate by Private Keys
2) Generate by Mnemonic
3) Encrypt raw -> keystore
4) Decrypt keystore -> raw
Press enter to exit
>
```

### 1. Generate by Private Keys

Генерирует случайные приватные ключи и ищет адреса, подходящие под паттерны из `configs/patterns.yaml`.

Можно выбрать:

- сохранять найденные приватные ключи в plaintext;
- шифровать найденные приватные ключи в keystore с паролем;
- сохранить подсказку к паролю в `hint.txt`.

Вывод:

- `logs/private/<DATE>/private_<TIME>/app.log`
- `logs/private/<DATE>/private_<TIME>/<kind>.jsonl`
- `logs/private/<DATE>/private_keystore_<TIME>/<kind>.jsonl` при генерации с keystore
- `logs/private/<DATE>/private_keystore_<TIME>/hint.txt` если указана подсказка

### 2. Generate by Mnemonic

Генерирует BIP-39 мнемоники, опционально использует passphrase и деривирует адреса по пути Ethereum:

```text
m/44'/60'/0'/0/<index>
```

По умолчанию деривируется 5 адресов на одну мнемонику.

Генерируются мнемоники из 12 слов. Результат содержит мнемонику, passphrase, путь деривации и приватный ключ открытым текстом. BIP-39 passphrase не шифрует файл результатов; доступ к этому файлу даёт доступ ко всем сохранённым в нём секретам.

С v1.0.1 passphrase нормализуется в Unicode NFKD согласно [BIP-39](https://github.com/bitcoin/bips/blob/master/bip-0039.mediawiki). В v1.0.0 нормализация отсутствовала: для старых passphrase с символами, меняющимися при NFKD (например, `é`), стандартное восстановление из мнемоники может дать другой адрес. Для таких ранее созданных кошельков используйте сохранённый приватный ключ; обновление не меняет старые файлы результатов.

Вывод:

- `logs/mnemonics/<DATE>/mnemonics_<TIME>/app.log`
- `logs/mnemonics/<DATE>/mnemonics_<TIME>/<kind>.log`
- `logs/mnemonics/<DATE>/mnemonics_<TIME>/hint.txt` если указана подсказка

### 3. Encrypt raw -> keystore

Читает приватные ключи из:

```text
inputs/encrypt/privates.txt
```

Формат:

```text
0x1234567890abcdef...
0xabcdef1234567890...
# комментарии игнорируются
```

Вывод:

- `logs/encrypt/<DATE>/encrypt_<TIME>/app.log`
- `logs/encrypt/<DATE>/encrypt_<TIME>/all.jsonl`
- `logs/encrypt/<DATE>/encrypt_<TIME>/files/<address>.json`
- `logs/encrypt/<DATE>/encrypt_<TIME>/hint.txt` если указана подсказка

### 4. Decrypt keystore -> raw

Читает keystore-файлы из `inputs/decrypt/`.

Поддерживаемые форматы:

- `inputs/decrypt/*.jsonl`
- `inputs/decrypt/*.json`
- `inputs/decrypt/files/*.json`

JSONL содержит один keystore-объект на строку. Можно копировать сюда `specific.jsonl` и другие JSONL-файлы генератора с включённым шифрованием. Обычный JSONL с полем `private_key` не является keystore. Не копируйте одновременно JSONL и отдельные JSON тех же кошельков, иначе получите повторяющиеся строки.

Вывод:

- `logs/decrypt/<DATE>/decrypt_<TIME>/app.log`
- `logs/decrypt/<DATE>/decrypt_<TIME>/all.txt`

Формат `all.txt`:

```text
address:private_key
```

При неверном пароле, повреждённом входном файле или ошибках обработки операция сообщает об ошибке; успешно обработанные записи остаются в результатах. Пустой ввод также считается ошибкой. Ctrl+C прекращает обработку после текущей криптографической операции.

Во всех путях `<DATE>` — локальная дата `DD.MM.YYYY`, `<TIME>` — `HH-MM-SS_<unique>`. Каждому запуску выделяется отдельная папка, включая одновременные запуски. Время внутри `app.log` выводится в UTC+03:00.

## Паттерны

### Specific

```yaml
specific:
  - prefix: "dead"
    suffix: "beef"
    final: false
```

Найдет адрес вида:

```text
0xdead...beef
```

### Symmetric

Можно использовать placeholder-паттерны `X` и `Y`:

```yaml
symmetric:
  - prefix: "XX"
    suffix: "YY"
    final: true
```

Также поддерживаются literal hex-паттерны:

```yaml
symmetric:
  - prefix: "1234"
    suffix: "4321"
    final: true
```

Одинаковый placeholder обозначает один и тот же символ по обеим сторонам: `XX…XX` требует четыре одинаковых символа, а `XX…YY` — две пары (они могут совпадать). Каждая часть должна состоять либо только из `X`/`Y`, либо только из hex-символов; смешивание `X` и `a` внутри одной части не поддерживается.

### Edges

```yaml
edges:
  minCount: 4
  side: "prefix"
  final: false
```

Найдет адреса вида:

```text
0xaaaa...
0x1111...
```

### Regexp

```yaml
regexp:
  - pattern: "(?i)^0x[a-f]{40}"
    final: false
```

Регулярные выражения применяются к полному адресу, включая `0x`.

Используется синтаксис Go regexp: обратные ссылки (`\1`) и lookaround не поддерживаются. Некорректный regexp отклоняется при загрузке конфигурации. Для повторяющихся символов на краях используйте `edges`; если `side` отсутствует, применяется `any`. `minCount: 0` отключает этот блок.

## Безопасность

- Ввод паролей и passphrase выполняется скрыто, без отображения введенных символов в консоли.
- Пароли вводятся в интерактивном терминале; передача их через pipe не поддерживается. Начиная с v1.0.1 пробелы в начале и конце сохраняются. В v1.0.0 они удалялись: для старого keystore вводите пароль без этих пробелов.
- При `hide_secrets_in_console: true` приватные ключи, мнемоники, passphrase и пароли не должны отображаться в консольных логах.
- Файловые логи и результаты могут содержать секреты. Не храните директорию `logs/` в публичных местах и не отправляйте ее третьим лицам.
- На Unix-подобных системах файлы с секретами создаются с ограниченными правами доступа.
- Подсказка к паролю (`hint.txt`) не должна содержать сам пароль или его очевидную часть.
- Go не дает надежной гарантии полной очистки строк с секретами из памяти, поэтому не стоит рассматривать очистку памяти как основную защиту.

## Структура проекта

```text
WalletTools/
├── cmd/
│   └── wallettools/
│       └── main.go
├── configs/
│   ├── app.yaml
│   └── patterns.yaml
├── internal/
│   ├── cli/
│   ├── crypto/
│   ├── generator/
│   ├── keystore/
│   ├── logsink/
│   ├── mnemonic/
│   ├── ops/
│   │   └── encdec/
│   └── patterns/
├── inputs/
│   ├── encrypt/
│   └── decrypt/
├── pkg/
│   ├── appcfg/
│   ├── config/
│   ├── i18n/
│   └── logx/
├── go.mod
└── go.sum
```

## Проверка проекта

```bash
go test ./...
go test -race ./...
go vet ./...
go build -buildvcs=false ./cmd/wallettools
```

При запуске через Go укажите папку проекта явно: `go run ./cmd/wallettools --data-dir .`.

`-race` требует CGO и C-компилятор. Проверки включают генерацию обоими способами, шифрование/дешифрование тестового ключа, неверный пароль, повреждённый keystore, маскирование логов, сохранность файлов при обновлении и валидность YAML-примеров этого README.

## Выпуск релизов

CI проверяет тесты, `go vet` и сборку на Windows и Linux. Отправка тега `vMAJOR.MINOR.PATCH` запускает `.github/workflows/release.yml`: тесты, проверка кода, сборка Windows x64 без CGO, проверка запуска и публикация EXE, ZIP и SHA-256 в GitHub Releases.

Перед выпуском обновите `docs/release-notes.md` и отправьте изменения в GitHub, затем создайте тег на нужном коммите:

```bash
git tag v1.0.1
git push origin v1.0.1
```

Локальная сборка релиза в PowerShell: `./scripts/build-release.ps1 -Version v1.0.1`. Результаты находятся в `dist/v1.0.1/`. В архив включаются только EXE, README и LICENSE; стартовые конфиги встроены в программу из `internal/portable/defaults/`, пользовательские конфиги, ключи и логи не упаковываются.

## Зависимости

- `github.com/ethereum/go-ethereum` - криптография Ethereum и keystore
- `github.com/miguelmota/go-ethereum-hdwallet` - HD wallet деривация
- `github.com/tyler-smith/go-bip39` - BIP-39 мнемоники
- `go.uber.org/zap` - логирование
- `golang.org/x/term` - скрытый ввод паролей
- `gopkg.in/yaml.v3` - YAML-конфиги

## Донат

Если проект оказался полезен, можно поддержать разработку EVM-донатом:

```text
EVM: 0x22225b48937dAa55D28f26D26cD09bE5b6E12222
```

## Лицензия

См. `LICENSE`.
