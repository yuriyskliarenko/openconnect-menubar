APP := dist/OpenConnect Menubar.app

.PHONY: app install test clean

app:
	./scripts/build-app.sh

install: app
	mkdir -p "$(HOME)/Applications"
	rm -rf "$(HOME)/Applications/OpenConnect Menubar.app"
	cp -R "$(APP)" "$(HOME)/Applications/"

test:
	go vet ./...
	go test ./...

clean:
	rm -rf dist
