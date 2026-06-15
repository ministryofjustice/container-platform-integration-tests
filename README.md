# Container Platform Integration Tests

[![Ministry of Justice Repository Compliance Badge](https://github-community.service.justice.gov.uk/repository-standards/api/template-repository/badge)](https://github-community.service.justice.gov.uk/repository-standards/template-repository)

## Introduction

This repository contains integration tests for Container Platform CP3.0 clusters and components.

## How to run Go tests

To run the integration tests on a MoJ Container Platform cluster you must have the following tools installed:

_TODO:_ (Tool versioning here notes here??)

- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [terraform](https://learn.hashicorp.com/tutorials/terraform/install-cli)
- [aws-cli](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
- [git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
- [Go](https://go.dev/doc/install)
- [Ginkgo v2](https://onsi.github.io/ginkgo/#installing-ginkgo)

You can then either run:

```bash
go test -v ./...
```

or

```bash
cd test; ginkgo -r -v  # for realtime response
```

### Running individual tests

A neat trick in Ginkgo is to place an "F" in front of the "Describe", "It" or "Context" functions. This marks it as [focused](https://onsi.github.io/ginkgo/#focused-specs).

So, if you have spec like:

```
    It("should be idempotent", func() {
```

You rewrite it as:

```
    FIt("should be idempotent", func() {
```

And it will run exactly that one spec:

```
[Fail] testing Migrate setCurrentDbVersion [It] should be idempotent
...
Ran 1 of 5 Specs in 0.003 seconds
FAIL! -- 0 Passed | 1 Failed | 0 Pending | 4 Skipped
```

### Making changes to Ginkgo tests

Ginkgo works best from the command-line, and [ginkgo watch](https://onsi.github.io/ginkgo/#watching-for-changes) makes it easy to rerun tests on the command line whenever changes are detected.

## How to update Go dependencies

With the repository cloned:

```bash
cd test; go get -u ./...
```

Perform the tests as outlined [above](#how-to-run-go-tests) and confirm they pass.

Create a PR and merge to main.
