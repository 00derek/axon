module github.com/axonframework/axon/session

go 1.26.2

require (
	github.com/axonframework/axon/axontest v0.0.0
	github.com/axonframework/axon/interfaces v0.0.0
	github.com/axonframework/axon/kernel v0.0.0
)

replace github.com/axonframework/axon/kernel => ../kernel

replace github.com/axonframework/axon/interfaces => ../interfaces

replace github.com/axonframework/axon/axontest => ../axontest
