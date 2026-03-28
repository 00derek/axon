module github.com/axonframework/axon/examples

go 1.25.2

require (
	github.com/axonframework/axon/kernel v0.0.0
	github.com/axonframework/axon/middleware v0.0.0
	github.com/axonframework/axon/workflow v0.0.0
	github.com/axonframework/axon/testing v0.0.0
	github.com/axonframework/axon/interfaces v0.0.0
	github.com/axonframework/axon/contrib/plan v0.0.0
)

replace (
	github.com/axonframework/axon/kernel => ../kernel
	github.com/axonframework/axon/middleware => ../middleware
	github.com/axonframework/axon/workflow => ../workflow
	github.com/axonframework/axon/testing => ../testing
	github.com/axonframework/axon/interfaces => ../interfaces
	github.com/axonframework/axon/contrib/plan => ../contrib/plan
)
