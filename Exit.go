/*
File Name:  Exit.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

// Exit codes signal why the application exited. These are universal between clients developed by the Peernet organization.
// Clients are encouraged to log additional details in a log file. 3rd party clients may define additional exit codes.
const (
	ExitSuccess            = 0          // This is actually never used.
	ExitErrorConfigAccess  = 1          // Error accessing the config file.
	ExitErrorConfigRead    = 2          // Error reading the config file.
	ExitErrorConfigParse   = 3          // Error parsing the config file.
	ExitErrorLogInit       = 4          // Error initializing log file.
	ExitParamWebapiInvalid = 5          // Parameter for webapi is invalid.
	ExitPrivateKeyCorrupt  = 6          // Private key is corrupt.
	ExitPrivateKeyCreate   = 7          // Cannot create a new private key.
	ExitBlockchainCorrupt  = 8          // Blockchain is corrupt.
	ExitGraceful           = 9          // Graceful shutdown.
	ExitParamApiKeyInvalid = 10         // API key parameter is invalid.
	STATUS_CONTROL_C_EXIT  = 0xC000013A // The application terminated as a result of a CTRL+C. This is a Windows NTSTATUS value.
)
