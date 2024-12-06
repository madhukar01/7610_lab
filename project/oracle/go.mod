module oracle

go 1.21

replace agent => ../agent

require (
	agent v0.0.0-00010101000000-000000000000
	github.com/sashabaranov/go-openai v1.36.0
)
