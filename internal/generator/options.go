package generator

type Source string

const (
	SourcePrivKey  Source = "private"
	SourceMnemonic Source = "mnemonics"
)

type Options struct {
	Source           Source
	Encrypt          bool
	KeystorePassword string

	WordsStrength int    // for mnemonic, 128=12 words
	DeriveN       int    // number of accounts to derive per mnemonic
	Passphrase    string // BIP-39 passphrase (not encryption!)

	LogsBase      string // logs
	PassHint      string // hint.txt
	PatternsPath  string // configs/patterns.yaml
	CaseMaskedOut bool   // console masking (handled by logx/masking_core)

	Workers int
}
