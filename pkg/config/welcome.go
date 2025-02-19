package config

import (
	"fmt"
)

func DisplayWelcomeMessage(multiAddr, ipAddr, publicKeyHex string, isStaked bool, isWriterNode bool) {
	// ANSI escape code for yellow text
	yellow := "\033[33m"
	// ANSI escape code for blue text
	blue := "\033[34m"
	// ANSI escape code to reset color
	reset := "\033[0m"

	borderLine := "#######################################"

	fmt.Println(yellow + borderLine + reset)
	fmt.Println(yellow + "#     __  __    _    ____    _        #" + reset)
	fmt.Println(yellow + "#    |  \\/  |  / \\  / ___|  / \\       #" + reset)
	fmt.Println(yellow + "#    | |\\/| | / _ \\ \\___ \\ / _ \\      #" + reset)
	fmt.Println(yellow + "#    | |  | |/ ___ \\ ___) / ___ \\     #" + reset)
	fmt.Println(yellow + "#    |_|  |_/_/   \\_\\____/_/   \\_\\    #" + reset)
	fmt.Println(yellow + "#                                     #" + reset)
	fmt.Println(yellow + borderLine + reset)

	// Displaying the multi-address and IP address in blue
	fmt.Printf(blue+"Multiaddress: %s\n"+reset, multiAddr)
	fmt.Printf(blue+"IP Address:   %s\n"+reset, ipAddr)
	fmt.Printf(blue+"Public Key:   %s\n"+reset, publicKeyHex)
	fmt.Printf(blue+"Is Staked:    %t\n"+reset, isStaked)
	fmt.Printf(blue+"Is Writer:    %t\n"+reset, isWriterNode)
}
