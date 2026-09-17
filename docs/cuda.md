# NVIDIA CUDA

WalletTools `v1.1.0` и новее выполняет генерацию приватных ключей, secp256k1, Keccak и
поиск совпадений непосредственно на NVIDIA GPU. CPU управляет CUDA-процессами,
перепроверяет найденный адрес и сохраняет результат.

## Требования для запуска

- Windows x64;
- NVIDIA GPU с compute capability `sm_75` или новее;
- актуальный драйвер NVIDIA;
- готовый `wallettools-windows-amd64.exe` из GitHub Releases.

CUDA Toolkit, Visual Studio и Go нужны только разработчику для пересборки. В
релизный EXE уже встроен fat CUDA backend для поддерживаемых архитектур.

## Включение GPU

Откройте `configs/app.yaml` рядом с программой:

```yaml
gpu_enabled: true
cuda_executable: "wallettools-cuda.exe"
cuda_device: 0
cuda_batch_size: 65536
```

Несмотря на значение `cuda_executable`, отдельный файл класть рядом не нужно.
Стандартное имя означает: использовать внешний файл, если он существует, иначе
извлечь встроенный helper в `%LOCALAPPDATA%\WalletTools\cuda\<hash>\`.

`cuda_device` — индекс видеокарты, начиная с нуля. `cuda_batch_size` влияет на
производительность и VRAM. Начните с `65536`; при нехватке памяти уменьшайте
значение. `cores` в GPU-режиме не включает CPU-генераторы.

## GPU-совместимый конфиг паттернов

```yaml
symbols: "A B C D E F 0 1 2 3 4 5 6 7 8 9"
case_sensitive: false

symmetric:
  - prefix: "XXXX"
    suffix: "YYYY"
    final: false

specific:
  - prefix: "dead"
    suffix: "beef"
    final: false

edges:
  minCount: 0
  side: "any"
  final: false

regexp:
  - pattern: "^0x[0-9]{4,6}[^0-9].*[^a-f][a-f]{4,6}$"
    final: true
```

Поддерживаются все уникальные `specific`, `symmetric` и `regexp` одновременно.
Повторяющиеся цели объединяются. Совпадение `final: false` сохраняется, после
чего поиск этой цели продолжается; `final: true` останавливает весь запуск.

Сейчас GPU-режим требует `case_sensitive: false` и `edges.minCount: 0`.
Регистрозависимый поиск и `edges` доступны в CPU-режиме.

## Regexp на GPU

Regexp применяется к полному адресу с `0x`. Поддерживаются:

- `^` и `$`;
- литералы и точка `.`;
- классы `[0-9a-f]` и отрицательные классы `[^0-9]`;
- группы и альтернация `foo|bar`;
- `?`, `*`, `+`, `{n}` и `{n,m}`;
- inline-флаг `(?i)`.

Используется синтаксис Go RE2. Backreference (`\1`), lookahead и lookbehind в
Go regexp отсутствуют и отклоняются ещё при загрузке YAML. Слишком сложное для
конечного развёртывания выражение также отклоняется вместо скрытого перехода на
CPU.

Примеры:

```yaml
regexp:
  # От 4 до 6 цифр в начале тела и от 4 до 6 букв a-f в конце.
  - pattern: "^0x[0-9]{4,6}[^0-9].*[^a-f][a-f]{4,6}$"
    final: true

  # Адрес начинается с dead или beef и заканчивается четырьмя цифрами.
  - pattern: "^0x(?:dead|beef).*[0-9]{4}$"
    final: false
```

Найденный по CUDA-маске кандидат всегда повторно проверяется исходным regexp на
CPU. Это необходимо, поскольку объединённая маска для альтернатив и переменной
длины может быть шире исходного выражения.

## Несколько целей и VRAM

Каждая уникальная цель запускается отдельным CUDA-процессом. При batch size
`65536` один процесс обычно резервирует около 2 ГиБ VRAM. Например, пять
уникальных целей могут потребовать около 10 ГиБ. Фактическое значение зависит
от backend, драйвера и модели GPU.

Если памяти недостаточно:

1. уменьшите количество одновременно активных паттернов;
2. уменьшите `cuda_batch_size`;
3. закройте приложения, использующие GPU;
4. перезапустите WalletTools.

## Диагностика

`no CUDA devices selected` — драйвер не видит подходящую NVIDIA GPU либо неверно
задан `cuda_device`.

`out of memory` — недостаточно VRAM для количества целей и выбранного batch
size.

`GPU mode does not support edges patterns yet` — установите
`edges.minCount: 0` или выключите GPU.

`GPU mode does not support case_sensitive addresses yet` — установите
`case_sensitive: false` или используйте CPU.

Ошибка компиляции regexp — проверьте выражение командой `go regexp`-совместимого
редактора и исключите backreference/lookaround.

Автоматического fallback на CPU нет. Чтобы явно использовать процессор:

```yaml
gpu_enabled: false
```

## Сборка из исходников

Для пересборки CUDA helper нужны CUDA Toolkit 13.x, Visual Studio 2022 Build
Tools с MSVC C++ и Go 1.25+:

```powershell
.\scripts\build-cuda.ps1 -Arch all
go test ./...
go build -buildvcs=false -trimpath -ldflags "-s -w" -o wallettools.exe ./cmd/wallettools
```

Для быстрой локальной сборки только под RTX 40xx можно использовать
`-Arch sm_89`. Публикуемый релиз необходимо собирать с `-Arch all`.

Модифицированные исходники backend находятся в `third_party/provanity`, условия
лицензии — в `THIRD_PARTY_NOTICES.md`.
