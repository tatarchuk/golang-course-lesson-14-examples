# Lesson 14 examples (WebSockets). The targets grow with the parts of the homework; `make` lists them.
.DEFAULT_GOAL := help

help:          ## list the targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

.PHONY: help
