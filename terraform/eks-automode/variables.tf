# -----------------------------------------------------------------------------
# EKS Auto Mode + Bedrock Deployment Variables
# -----------------------------------------------------------------------------

variable "region" {
  description = "AWS region for EKS cluster and Bedrock"
  type        = string
  default     = "us-east-1"
}

variable "cluster_name" {
  description = "EKS cluster name"
  type        = string
  default     = "karo-cluster"
}

variable "cluster_version" {
  description = "Kubernetes version for EKS"
  type        = string
  default     = "1.32"
}

variable "vpc_cidr" {
  description = "VPC CIDR block"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "Availability zones (minimum 2)"
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b", "us-east-1c"]
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for private subnets (one per AZ)"
  type        = list(string)
  default     = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for public subnets (one per AZ)"
  type        = list(string)
  default     = ["10.0.101.0/24", "10.0.102.0/24", "10.0.103.0/24"]
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

variable "bedrock_model_id" {
  description = "Bedrock model ID"
  type        = string
  default     = "anthropic.claude-sonnet-4-20250514-v1:0"
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

variable "tags" {
  description = "Tags to apply to all AWS resources"
  type        = map(string)
  default = {
    Project   = "karo"
    ManagedBy = "opentofu"
  }
}
