package i18n

type Messages struct {
	AppTitle            string
	MenuTitle           string
	MenuGenPrivKeys     string
	MenuGenMnemonics    string
	MenuEncryptRaw      string
	MenuDecryptKeystore string
	MenuShowPatterns    string
	MenuExit            string
	UnknownCommand      string
	ExitSelected        string
	ExitText            string
	GenPrivPrompt       string
	GenPrivStarted      string
	GenPrivStub         string
	GenMnemPrompt       string
	GenMnemStarted      string
	GenMnemStub         string
	EncryptPrompt       string
	EncryptStdin        string
	EncryptPlanned      string
	DecryptPrompt       string
	DecryptPlanned      string
	ConfigNotLoaded     string
	ConfigHeader        string
	ConfigSymbols       string
	ConfigSymmetric     string
	ConfigSpecific      string
	ConfigEdges         string
	ConfigRegexp        string
	ConfigCaseSensitive string
}

func Get(lang string) Messages {
	switch lang {
	case "en":
		return Messages{
			AppTitle:            "WalletTools — start menu",
			MenuTitle:           "WalletTools — start menu",
			MenuGenPrivKeys:     "1) Generate pattern via private keys (keystore on/off)",
			MenuGenMnemonics:    "2) Generate pattern via mnemonics (passphrase on/off)",
			MenuEncryptRaw:      "3) Encrypt raw private key -> keystore",
			MenuDecryptKeystore: "4) Decrypt keystore -> raw",
			MenuShowPatterns:    "5) Show loaded patterns (configs/patterns.yaml)",
			MenuExit:            "0) Exit",
			UnknownCommand:      "Unknown command:",
			ExitSelected:        "exit selected",
			ExitText:            "Exit",
			GenPrivPrompt:       "Generation via private keys. Encrypt keystore? (y/n)",
			GenPrivStarted:      "genPrivKeys started",
			GenPrivStub:         "Started (stub). Keystore encryption: %v\n",
			GenMnemPrompt:       "Generation via mnemonics. Use passphrase? (y/n)",
			GenMnemStarted:      "genMnemonics started",
			GenMnemStub:         "Started (stub). Passphrase: %v\n",
			EncryptPrompt:       "Encrypt raw private key -> keystore (stub).\nEnter path to file with private keys (or press Enter for stdin):",
			EncryptStdin:        "Waiting for stdin (not implemented in stub).",
			EncryptPlanned:      "Will be encrypted file: %s (stub)\n",
			DecryptPrompt:       "Decrypt keystore -> raw (stub).\nEnter path to keystore file/dir:",
			DecryptPlanned:      "Will be decrypted: %s (stub)\n",
			ConfigNotLoaded:     "Config not loaded",
			ConfigHeader:        "=== patterns config ===",
			ConfigSymbols:       "Symbols: %s\n",
			ConfigSymmetric:     "Symmetric:",
			ConfigSpecific:      "Specific:",
			ConfigEdges:         "Edges: minCount=%d side=%s final=%v\n",
			ConfigRegexp:        "Regexp:",
			ConfigCaseSensitive: "Case sensitive: %v\n",
		}
	default: // "ru"
		return Messages{
			AppTitle:            "WalletTools — стартовое меню",
			MenuTitle:           "WalletTools — стартовое меню",
			MenuGenPrivKeys:     "1) Генерация нужного паттерна через приватные ключи (keystore on/off)",
			MenuGenMnemonics:    "2) Генерация нужного паттерна через мнемоники (passphrase on/off)",
			MenuEncryptRaw:      "3) Шифрация raw приватного ключа в keystore",
			MenuDecryptKeystore: "4) Дешифрация приватного ключа из keystore -> raw",
			MenuShowPatterns:    "5) Показать загруженные patterns (configs/patterns.yaml)",
			MenuExit:            "0) Выход",
			UnknownCommand:      "Неизвестная команда:",
			ExitSelected:        "exit selected",
			ExitText:            "Выход",
			GenPrivPrompt:       "Генерация по приватным ключам. Опция: шифровать keystore? (y/n)",
			GenPrivStarted:      "genPrivKeys started",
			GenPrivStub:         "Запущена (заглушка). Шифрование keystore: %v\n",
			GenMnemPrompt:       "Генерация по мнемоникам. Опция: использовать passphrase? (y/n)",
			GenMnemStarted:      "genMnemonics started",
			GenMnemStub:         "Запущена (заглушка). Passphrase: %v\n",
			EncryptPrompt:       "Шифрация raw приватного ключа -> keystore (заглушка).\nУкажи путь к файлу с приватными ключами (или нажми Enter для stdin):",
			EncryptStdin:        "Ожидание stdin (не реализовано в заглушке).",
			EncryptPlanned:      "Будет зашифрован файл: %s (заглушка)\n",
			DecryptPrompt:       "Дешифрация keystore -> raw (заглушка).\nУкажи путь к папке/файлу keystore:",
			DecryptPlanned:      "Будет расшифрован: %s (заглушка)\n",
			ConfigNotLoaded:     "Config не загружен",
			ConfigHeader:        "=== patterns config ===",
			ConfigSymbols:       "Symbols: %s\n",
			ConfigSymmetric:     "Symmetric:",
			ConfigSpecific:      "Specific:",
			ConfigEdges:         "Edges: minCount=%d side=%s final=%v\n",
			ConfigRegexp:        "Regexp:",
			ConfigCaseSensitive: "Чувствительность к регистру: %v\n",
		}
	}
}
