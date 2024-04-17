# Dev notes

## Project init
```shell
git init .
go mod init github.com/bcollard/porthole
```

## KO (k8s tooling for Go devs)
```shell
k create deployment porthole -n default --image ko://github.com/bcollard/porthole/main --dry-run -o yaml > deploy/deployment-ko.yaml
# then check out the makefile targets
```


