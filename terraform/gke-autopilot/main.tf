# -----------------------------------------------------------------------------
# GKE Autopilot + Vertex AI — KARO Deployment
# -----------------------------------------------------------------------------

provider "google" {
  project = var.project_id
  region  = var.region
}

provider "google-beta" {
  project = var.project_id
  region  = var.region
}

provider "kubernetes" {
  host                   = "https://${google_container_cluster.karo.endpoint}"
  token                  = data.google_client_config.default.access_token
  cluster_ca_certificate = base64decode(google_container_cluster.karo.master_auth[0].cluster_ca_certificate)
}

provider "helm" {
  kubernetes {
    host                   = "https://${google_container_cluster.karo.endpoint}"
    token                  = data.google_client_config.default.access_token
    cluster_ca_certificate = base64decode(google_container_cluster.karo.master_auth[0].cluster_ca_certificate)
  }
}

data "google_client_config" "default" {}

# -----------------------------------------------------------------------------
# Enable required GCP APIs
# -----------------------------------------------------------------------------
resource "google_project_service" "apis" {
  for_each = toset([
    "container.googleapis.com",
    "aiplatform.googleapis.com",
    "iam.googleapis.com",
    "compute.googleapis.com",
  ])
  project            = var.project_id
  service            = each.value
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# VPC Network
# -----------------------------------------------------------------------------
resource "google_compute_network" "karo" {
  name                    = var.network_name
  auto_create_subnetworks = false
  depends_on              = [google_project_service.apis]
}

resource "google_compute_subnetwork" "karo" {
  name          = var.subnet_name
  ip_cidr_range = var.subnet_cidr
  region        = var.region
  network       = google_compute_network.karo.id

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = var.pods_cidr
  }

  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = var.services_cidr
  }

  private_ip_google_access = true
}

# Cloud NAT for outbound internet from pods
resource "google_compute_router" "karo" {
  name    = "${var.cluster_name}-router"
  region  = var.region
  network = google_compute_network.karo.id
}

resource "google_compute_router_nat" "karo" {
  name                               = "${var.cluster_name}-nat"
  router                             = google_compute_router.karo.name
  region                             = var.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"
}

# -----------------------------------------------------------------------------
# GKE Autopilot Cluster
# -----------------------------------------------------------------------------
resource "google_container_cluster" "karo" {
  provider = google-beta

  name     = var.cluster_name
  location = var.region

  # Autopilot mode
  enable_autopilot = true

  network    = google_compute_network.karo.id
  subnetwork = google_compute_subnetwork.karo.id

  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  # Private cluster with public endpoint
  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
    master_ipv4_cidr_block  = "172.16.0.0/28"
  }

  dynamic "master_authorized_networks_config" {
    for_each = length(var.master_authorized_networks) > 0 ? [1] : []
    content {
      dynamic "cidr_blocks" {
        for_each = var.master_authorized_networks
        content {
          cidr_block   = cidr_blocks.value.cidr_block
          display_name = cidr_blocks.value.display_name
        }
      }
    }
  }

  # Workload Identity (required for Vertex AI auth)
  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  release_channel {
    channel = "REGULAR"
  }

  deletion_protection = false

  depends_on = [google_project_service.apis]
}

# -----------------------------------------------------------------------------
# Workload Identity — GCP Service Account for Vertex AI
# -----------------------------------------------------------------------------
resource "google_service_account" "karo_agent" {
  account_id   = "karo-agent-vertex"
  display_name = "KARO Agent Vertex AI Access"
}

# Grant the GSA permission to call Vertex AI
resource "google_project_iam_member" "agent_vertex_user" {
  project = var.project_id
  role    = "roles/aiplatform.user"
  member  = "serviceAccount:${google_service_account.karo_agent.email}"
}

# Workload Identity binding: allow the KSA to impersonate the GSA
resource "google_service_account_iam_member" "karo_agent_wi_binding" {
  service_account_id = google_service_account.karo_agent.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[${var.agent_namespace}/karo-agent]"
}

# KSA for agent pods — annotated for Workload Identity
resource "kubernetes_service_account" "karo_agent" {
  metadata {
    name      = "karo-agent"
    namespace = kubernetes_namespace.agents.metadata[0].name
    annotations = {
      "iam.gke.io/gcp-service-account" = google_service_account.karo_agent.email
    }
  }
}

# -----------------------------------------------------------------------------
# Namespaces
# -----------------------------------------------------------------------------
resource "kubernetes_namespace" "karo_system" {
  metadata {
    name = var.karo_namespace
  }
  depends_on = [google_container_cluster.karo]
}

resource "kubernetes_namespace" "agents" {
  metadata {
    name = var.agent_namespace
    labels = {
      "karo.dev/managed" = "true"
    }
  }
  depends_on = [google_container_cluster.karo]
}

# -----------------------------------------------------------------------------
# KARO Operator — Helm Release
# -----------------------------------------------------------------------------
resource "helm_release" "karo" {
  name       = "karo"
  namespace  = kubernetes_namespace.karo_system.metadata[0].name
  chart      = var.karo_helm_chart_path
  wait       = true
  timeout    = 600

  set {
    name  = "image.repository"
    value = split(":", var.karo_operator_image)[0]
  }
  set {
    name  = "image.tag"
    value = try(split(":", var.karo_operator_image)[1], "latest")
  }
  set {
    name  = "replicaCount"
    value = "2"
  }
}

# -----------------------------------------------------------------------------
# Outputs
# -----------------------------------------------------------------------------
output "cluster_name" {
  value = google_container_cluster.karo.name
}

output "cluster_endpoint" {
  value     = google_container_cluster.karo.endpoint
  sensitive = true
}

output "kubeconfig_command" {
  value = "gcloud container clusters get-credentials ${google_container_cluster.karo.name} --region ${var.region} --project ${var.project_id}"
}

output "vertex_service_account" {
  value = google_service_account.karo_agent.email
}
