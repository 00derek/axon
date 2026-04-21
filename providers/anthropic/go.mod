module github.com/axonframework/axon/providers/anthropic

go 1.26.2

require (
	github.com/anthropics/anthropic-sdk-go v1.37.0
	github.com/axonframework/axon/kernel v0.0.0
)

require (
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	golang.org/x/sync v0.16.0 // indirect
)

replace github.com/axonframework/axon/kernel => ../../kernel
