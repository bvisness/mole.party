build: ## Rebuild the app binary
	go build -o bin/mole .

pull: ## Pull the latest code from GitHub
	git fetch --all
	git reset --hard origin/main

deploy: pull build ## Rebuild and restart the app
	sudo systemctl restart mole

.PHONY: build pull deploy
