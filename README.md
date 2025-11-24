# WalletTools

Инструмент для генерации кастомных EVM-кошельков (vanity addresses) с использованием паттернов, а также шифрования/дешифрования приватных ключей в формате keystore.

## Возможности

- **Генерация по приватным ключам**: Создание случайных приватных ключей с поиском адресов по заданным паттернам
- **Генерация по мнемоникам**: Генерация BIP-39 мнемоник с опциональной passphrase и деривацией нескольких адресов
- **Шифрование**: Преобразование приватных ключей в защищенные keystore-файлы
- **Дешифрование**: Извлечение приватных ключей из keystore-файлов
- **Многопоточность**: Настраиваемое количество воркеров для ускорения генерации
- **Паттерны**: Поддержка симметричных префиксов/суффиксов, специфичных строк, регулярных выражений
- **Безопасность**: Скрытие секретных данных в логах (опционально)

## Установка

### Требования

- Go 1.24.0 или выше
- Windows/Linux/macOS

### Сборка

  ```bash
  git clone <repository-url>
  cd WalletTools
  go mod download
  go build -o wallettools.exe ./cmd/wallettools

  Конфигурация

  configs/app.yaml

  Основные настройки приложения:

  # Язык интерфейса: "ru" или "en"                                                                                                                                                                                                   
  language: "ru"                                                                                                                                                                                                                     

  # Уровень логирования: debug | info | warn | error                                                                                                                                                                                 
  log_level: "info"                                                                                                                                                                                                                  

  # Скрывать секретные данные в логах консоли                                                                                                                                                                                        
  hide_secrets: false                                                                                                                                                                                                                

  # Количество логических процессоров для генерации                                                                                                                                                                                  
  # 0 или не указано — использовать все доступные                                                                                                                                                                                    
  cores: 8                                                                                                                                                                                                                           

  configs/patterns.yaml

  Паттерны для поиска адресов:

  # Поддерживаемые символы в EVM-адресах                                                                                                                                                                                             
  symbols: "A B C D E F 0 1 2 3 4 5 6 7 8 9"                                                                                                                                                                                         
  case_sensitive: false                                                                                                                                                                                                              

  # Симметричные паттерны (префикс = суффикс в обратном порядке)                                                                                                                                                                     
  symmetric:                                                                                                                                                                                                                         
    - prefix: "XX"                                                                                                                                                                                                                   
      suffix: "YY"                                                                                                                                                                                                                   
      final: true  # остановить генерацию после первого найденного                                                                                                                                                                   

  # Специфичные паттерны                                                                                                                                                                                                             
  specific:                                                                                                                                                                                                                          
    - prefix: "beef"                                                                                                                                                                                                                 
      suffix: ""                                                                                                                                                                                                                     
      final: false                                                                                                                                                                                                                   
    - prefix: "0000"                                                                                                                                                                                                                 
      suffix: "0000"                                                                                                                                                                                                                 
      final: false                                                                                                                                                                                                                   

  # Граничные символы (повторяющиеся в начале/конце)                                                                                                                                                                                 
  edges:                                                                                                                                                                                                                             
    minCount: 3                                                                                                                                                                                                                      
    side: "any"  # any | prefix | suffix                                                                                                                                                                                             
    final: false                                                                                                                                                                                                                     

  # Регулярные выражения                                                                                                                                                                                                             
  regexp:                                                                                                                                                                                                                            
    - pattern: "(?i)^0x([a-f0-9])\\1\\1\\1"  # 4 одинаковых символа после 0x                                                                                                                                                         
      final: false                                                                                                                                                                                                                   
    - pattern: "(?i)face.{0,30}beef"  # FACE...BEEF                                                                                                                                                                                  
      final: true                                                                                                                                                                                                                    

  Использование

  Запуск

  ./wallettools.exe

  Появится интерактивное меню:

  WalletTools — Vanity generator
  1) Generate by Private Keys
  2) Generate by Mnemonic
  3) Encrypt raw → keystore
  4) Decrypt keystore → raw
  Press enter to exit
  >

  1. Генерация по приватным ключам

  Генерирует случайные приватные ключи и ищет адреса, соответствующие паттернам из configs/patterns.yaml.

  Опции:
  - Шифрование в keystore (с паролем и подсказкой)
  - Сохранение в чистом виде (только адреса и приватные ключи)

  Вывод:
  - logs/private/<DATE>/private_<TIME>/app.log — журнал работы
  - logs/private/<DATE>/private_<TIME>/<kind>.jsonl — найденные кошельки
  - logs/private/<DATE>/private_<TIME>/hint.txt — подсказка к паролю (если указана)

  2. Генерация по мнемоникам

  Генерирует BIP-39 мнемоники и деривирует адреса по стандартному пути Ethereum.

  Опции:
  - BIP-39 passphrase (опционально, с подтверждением)
  - Количество деривируемых адресов (по умолчанию 5)

  Вывод:
  - logs/mnemonic/<DATE>/mnemonic_<TIME>/app.log                                                                                                                                                                                     
  - logs/mnemonic/<DATE>/mnemonic_<TIME>/<kind>.txt — найденные мнемоники с адресами
  - logs/mnemonic/<DATE>/mnemonic_<TIME>/hint.txt — подсказка к passphrase

  3. Шифрование (Encrypt raw → keystore)

  Читает приватные ключи из inputs/encrypt/privates.txt и шифрует их в keystore-файлы.

  Формат inputs/encrypt/privates.txt:
  0x1234567890abcdef...
  0xabcdef1234567890...
  # комментарии игнорируются

  Вывод:
  - logs/encrypt/<DATE>/encrypt_<TIME>/all.jsonl — все keystore-файлы построчно
  - logs/encrypt/<DATE>/encrypt_<TIME>/files/<address>.json — отдельные keystore-файлы

  4. Дешифрование (Decrypt keystore → raw)

  Извлекает приватные ключи из keystore-файлов в директории inputs/decrypt/.

  Поддерживаемые форматы:
  - inputs/decrypt/all.jsonl — файл с построчными JSON
  - inputs/decrypt/*.json — отдельные keystore-файлы
  - inputs/decrypt/files/*.json — keystore-файлы в поддиректории

  Вывод:
  - logs/decrypt/<DATE>/decrypt_<TIME>/all.txt — формат address:private_key                                                                                                                                                          

  Структура проекта

  WalletTools/
  ├── cmd/
  │   └── wallettools/
  │       └── main.go              # Точка входа
  ├── configs/
  │   ├── app.yaml                 # Конфигурация приложения
  │   └── patterns.yaml            # Паттерны для поиска
  ├── internal/
  │   ├── cli/
  │   │   └── runner.go            # Интерактивный CLI
  │   ├── crypto/
  │   │   └── evm.go               # Работа с ключами и адресами
  │   ├── generator/
  │   │   ├── engine.go            # Генерация с паттернами
  │   │   └── options.go           # Опции генератора
  │   ├── keystore/
  │   │   └── sink.go              # Запись keystore-файлов
  │   ├── logsink/
  │   │   ├── fs.go                # Файловая система для логов
  │   │   └── write.go             # Запись совпадений
  │   ├── mnemonic/
  │   │   └── bip39.go             # Работа с BIP-39
  │   ├── ops/
  │   │   └── encdec/
  │   │       └── encdec.go        # Шифрование/дешифрование
  │   └── patterns/
  │       └── matcher.go           # Сопоставление с паттернами
  ├── pkg/
  │   ├── appcfg/
  │   │   └── appcfg.go            # Загрузка app.yaml
  │   ├── config/
  │   │   └── patterns.go          # Загрузка patterns.yaml
  │   ├── i18n/
  │   │   └── i18n.go              # Интернационализация
  │   └── logx/
  │       ├── logx.go              # Логирование (zap)
  │       └── masking_core.go      # Маскировка секретов
  ├── inputs/                      # Входные данные (создается пользователем)
  │   ├── encrypt/
  │   │   └── privates.txt         # Приватные ключи для шифрования
  │   └── decrypt/                 # Keystore-файлы для дешифрования
  ├── logs/                        # Выходные данные (создается автоматически)
  ├── go.mod
  └── go.sum

  Безопасность

  Рекомендации

  1. Пароли keystore: Используйте сложные пароли для защиты keystore-файлов
  2. Скрытие секретов: Включите hide_secrets: true в configs/app.yaml для скрытия приватных ключей и паролей в консоли
  3. Хранение логов: Логи с приватными ключами и мнемониками хранятся в директории logs/ — защитите эту директорию
  4. Очистка памяти: Пароли в памяти перезаписываются нулями после использования (wipeBytes)
  5. Ввод паролей: Все пароли вводятся в скрытом режиме (без отображения на экране)

  Особенности

  - Приватные ключи в plaintext сохраняются только при явном отказе от шифрования
  - Логи маскируют секретные данные при hide_secrets: true                                                                                                                                                                           
  - Поддержка подсказок к паролям (hint.txt) для упрощения запоминания

  Производительность

  - Многопоточность: Настраивается через параметр cores в configs/app.yaml                                                                                                                                                           
  - Прогресс: Каждые 10 секунд выводится статистика (количество попыток, скорость генерации)
  - Остановка: Нажмите Ctrl+C для корректного завершения работы
  - Final паттерны: Генерация останавливается автоматически после нахождения паттерна с final: true                                                                                                                                  

  Примеры паттернов

  Префиксы и суффиксы

  specific:                                                                                                                                                                                                                          
    - prefix: "dead"                                                                                                                                                                                                                 
      suffix: "beef"                                                                                                                                                                                                                 
      final: false                                                                                                                                                                                                                   
  Найдет: 0xdead...beef                                                                                                                                                                                                              

  Симметричные адреса

  symmetric:                                                                                                                                                                                                                         
    - prefix: "1234"                                                                                                                                                                                                                 
      suffix: "4321"                                                                                                                                                                                                                 
      final: true                                                                                                                                                                                                                    
  Найдет: 0x1234...4321                                                                                                                                                                                                              

  Регулярные выражения

  regexp:                                                                                                                                                                                                                            
    - pattern: "(?i)^0x[a-f]{40}"  # только буквы, без цифр                                                                                                                                                                          
      final: false                                                                                                                                                                                                                   

  Повторяющиеся символы

  edges:                                                                                                                                                                                                                             
    minCount: 4                                                                                                                                                                                                                      
    side: "prefix"                                                                                                                                                                                                                   
    final: false                                                                                                                                                                                                                     
  Найдет: 0xaaaa..., 0x1111..., и т.д.

  Зависимости

  - github.com/ethereum/go-ethereum — криптография Ethereum
  - github.com/miguelmota/go-ethereum-hdwallet — HD-кошельки
  - github.com/tyler-smith/go-bip39 — BIP-39 мнемоники
  - go.uber.org/zap — структурированное логирование
  - golang.org/x/term — скрытый ввод паролей
  - gopkg.in/yaml.v3 — парсинг YAML

  Лицензия

  См. LICENSE

  Поддержка

  При возникновении проблем или вопросов создайте issue в репозитории проекта.
  ```