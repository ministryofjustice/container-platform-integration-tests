# Container Platform Terraform Module Template

[![Ministry of Justice Repository Compliance Badge](https://github-community.service.justice.gov.uk/repository-standards/api/container-platform-terraform-template/badge)](https://github-community.service.justice.gov.uk/repository-standards/container-platform-terraform-template)

A template repository for building Terraform modules for the Container Platform.

## Usage

Click **"Use this template"** to create a new Terraform module repository.

## Structure

```
├── main.tf           # Main module resources
├── variables.tf      # Input variables
├── outputs.tf        # Output values
├── versions.tf       # Provider and Terraform version constraints
└── README.md
```

## After Creating Your Module

1. Update this README with your module's documentation
2. Update `CODEOWNERS` with the appropriate team
3. Review `dependabot.yml` configuration
4. Update the compliance badge URL with your repository name
5. Add your Terraform resources to `main.tf`
6. Define input variables in `variables.tf`
7. Define outputs in `outputs.tf`
8. Set version constraints in `versions.tf`

## Requirements

| Name | Version |
|------|---------|
| terraform | >= 1.0 |

## Repository Standards

This repository follows the [Ministry of Justice GitHub Repository Standards](https://github-community.service.justice.gov.uk/repository-standards/guidance).

## License

[MIT License](LICENSE)
