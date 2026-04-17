# -----------------------------------------------------------------------------
# GKE Autopilot + Vertex AI Deployment Variables
# -----------------------------------------------------------------------------

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region for GKE cluster and Vertex AI"
  type        = string
  default     = "us-central1"
}

variable "cluster_name" {
  description = "GKE Autopilot cluster name"
  type        = string
  default     = "karo-cluster"
}

variable "network_name" {
  description = "VPC network name"
  type        = string
  default     = "karo-vpc"
}

variable "subnet_name" {
  description = "Subnet name"
  type        = string
  default     = "karo-subnet"
}

variable "subnet_cidr" {
  description = "Primary CIDR range for the subnet"
  type        = string
  default     = "10.0.0.0/20"
}

variable "pods_cidr" {
  description = "Secondary CIDR range for pods"
  type        = string
  default     = "10.16.0.0/14"
}

variable "services_cidr" {
  description = "Secondary CIDR range for services"
  type        = string
  default     = "10.20.0.0/20"
}

variable "master_authorized_networks" {
  description = "CIDR blocks authorized to access the GKE master endpoint"
  type = list(object({
    cidr_block   = string
    display_name = string
  }))
  default = []
}

variable "karo_namespace" {
  description = "Kubernetes namespace for the KARO operator"
  type        = string
  default     = "karo-system"
}

variable "agent_namespace" {
  description = "Kubernetes namespace for example agent teams"
  type        = string
  default     = "karo-agents"
}

variable "vertex_ai_model" {
  description = "Vertex AI model name (Gemini 4)"
  type        = string
  default     = "gemini-4.0-flash"
}

variable "karo_operator_image" {
  description = "KARO operator container image"
  type        = string
  default     = "ghcr.io/karo-dev/karo-controller:v0.4.0-alpha"
}

variable "claude_code_harness_image" {
  description = "Claude Code harness container image"
  type        = string
  default     = "ghcr.io/karo-dev/karo-claude-code-harness:latest"
}

variable "karo_helm_chart_path" {
  description = "Path to the KARO Helm chart (local or remote)"
  type        = string
  default     = "../../charts/karo"
}

variable "github_token" {
  description = "GitHub token for agent workspace credentials"
  type        = string
  sensitive   = true
  default     = ""
}

variable "mem0_api_key" {
  description = "mem0 API key for shared agent memory"
  type        = string
  sensitive   = true
  default     = ""
}

variable "slack_bot_token" {
  description = "Slack bot token for AgentChannel"
  type        = string
  sensitive   = true
  default     = ""
}

variable "slack_signing_secret" {
  description = "Slack signing secret for AgentChannel"
  type        = string
  sensitive   = true
  default     = ""
}

variable "enable_example_agents" {
  description = "Deploy example agent teams"
  type        = bool
  default     = true
}
