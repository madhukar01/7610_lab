module github.com/mhollas/7610/oracle

go 1.21

require (
	github.com/mhollas/7610/agent v0.0.0-00010101000000-000000000000
	github.com/sashabaranov/go-openai v1.36.0
)

replace (
	github.com/mhollas/7610/agent => ../agent
	github.com/mhollas/7610/agent/llm => ../agent/llm
	github.com/mhollas/7610/config => ../config
)
