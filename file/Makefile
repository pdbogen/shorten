upload: .upload
.upload: shorten
	aws s3 cp shorten s3://cernu-us-bins/shorten
	date +%s > version
	aws s3 cp version s3://cernu-us-bins/shorten-version
	rm -f version
	touch .upload

shorten: main.go
	go build -o shorten .
