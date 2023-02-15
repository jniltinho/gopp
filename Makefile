build: gopp.go config.go
	go mod tidy
	go build -ldflags='-s -w' $+
	help2man --no-discard-stderr --name='Postfix policy provider' -v-v -s8 -N -ogopp.8 ./gopp

install: build
	sudo install -s -m 0555 -o root -g bin -T gopp /usr/local/sbin/gopp
	sudo mkdir -p /usr/local/share/man/man8
	sudo gzip -c gopp.8 > /usr/local/share/man/man8/gopp.8.gz
