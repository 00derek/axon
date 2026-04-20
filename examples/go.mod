module github.com/axonframework/axon/examples

go 1.25.2

require (
	github.com/anthropics/anthropic-sdk-go v1.37.0
	github.com/axonframework/axon/interfaces v0.0.0
	github.com/axonframework/axon/kernel v0.0.0
	github.com/axonframework/axon/middleware v0.0.0
	github.com/axonframework/axon/plan v0.0.0
	github.com/axonframework/axon/providers/anthropic v0.0.0
	github.com/axonframework/axon/providers/openai v0.0.0
	github.com/axonframework/axon/testing v0.0.0
	github.com/axonframework/axon/workflow v0.0.0
	github.com/openai/openai-go/v3 v3.32.0
)

require (
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	golang.org/x/sync v0.16.0 // indirect
)

replace (
	github.com/axonframework/axon/interfaces => ../interfaces
	github.com/axonframework/axon/kernel => ../kernel
	github.com/axonframework/axon/middleware => ../middleware
	github.com/axonframework/axon/plan => ../plan
	github.com/axonframework/axon/providers/anthropic => ../providers/anthropic
	github.com/axonframework/axon/providers/openai => ../providers/openai
	github.com/axonframework/axon/testing => ../testing
	github.com/axonframework/axon/workflow => ../workflow
)
