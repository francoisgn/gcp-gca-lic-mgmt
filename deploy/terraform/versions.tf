terraform {
  required_version = ">= 1.6"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.6"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "~> 8.6"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.6"
    }
  }
  # Configure a remote state before the first apply, e.g.:
  # backend "gcs" { bucket = "my-tf-state" prefix = "gca-license-portal" }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

provider "google-beta" {
  project = var.project_id
  region  = var.region
}
