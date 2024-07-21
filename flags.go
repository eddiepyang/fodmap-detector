package main

var Opts struct {

	// Example of a callback, called each time the option is found.
	Model string `short:"m" long:"model" description:"model to use"`
}
