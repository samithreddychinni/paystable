package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/IDEA-Amrita/paystable/internal/secrets"
)

//go:embed env.template
var envTemplate string

func runInit(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			printInitUsage()
			return nil
		default:
			return fmt.Errorf("unknown init option: %s", args[0])
		}
	}

	const envPath = ".env"

	if _, err := os.Stat(envPath); err == nil {
		warnLine(".env already exists — refusing to overwrite")
		infoLine("remove or rename .env first, then run: ./paystable init")
		return fmt.Errorf(".env already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat .env: %w", err)
	}

	generated, err := secrets.GenerateAll()
	if err != nil {
		return fmt.Errorf("generate secrets: %w", err)
	}

	content := envTemplate
	for key, val := range generated {
		content = strings.ReplaceAll(content, "__"+key+"__", val)
	}

	if strings.Contains(content, "__") {
		return fmt.Errorf("env template still has unsubstituted placeholders")
	}

	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}

	okLine("wrote .env with generated local secrets")
	infoLine("update DATABASE_URL password to match Postgres (CHANGE_ME)")
	infoLine("select GATEWAY and set its credentials in .env")
	infoLine("next: ./paystable doctor")

	return nil
}

func printInitUsage() {
	fmt.Println("usage: paystable init")
	fmt.Println()
	fmt.Println("creates a local .env with generated secrets")
	fmt.Println("refuses to overwrite an existing .env")
}
