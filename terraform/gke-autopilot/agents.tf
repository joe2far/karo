# -----------------------------------------------------------------------------
# Example Agent Team — Vertex AI Gemini 4 + Claude Code Harness (GKE Autopilot)
# -----------------------------------------------------------------------------

# Secrets for agent credentials
resource "kubernetes_secret" "github_token" {
  count = var.enable_example_agents && var.github_token != "" ? 1 : 0
  metadata {
    name      = "github-token"
    namespace = kubernetes_namespace.agents.metadata[0].name
  }
  data = {
    GITHUB_TOKEN = var.github_token
  }
}

resource "kubernetes_secret" "mem0_api_key" {
  count = var.enable_example_agents && var.mem0_api_key != "" ? 1 : 0
  metadata {
    name      = "mem0-api-key"
    namespace = kubernetes_namespace.agents.metadata[0].name
  }
  data = {
    API_KEY = var.mem0_api_key
  }
}

resource "kubernetes_secret" "slack_credentials" {
  count = var.enable_example_agents && var.slack_bot_token != "" ? 1 : 0
  metadata {
    name      = "slack-app-credentials"
    namespace = kubernetes_namespace.agents.metadata[0].name
  }
  data = {
    BOT_TOKEN = var.slack_bot_token
  }
}

resource "kubernetes_secret" "slack_signing" {
  count = var.enable_example_agents && var.slack_signing_secret != "" ? 1 : 0
  metadata {
    name      = "slack-signing-secret"
    namespace = kubernetes_namespace.agents.metadata[0].name
  }
  data = {
    SIGNING_SECRET = var.slack_signing_secret
  }
}

# ConfigMaps for agent system prompts
resource "kubernetes_config_map" "planner_prompt" {
  count = var.enable_example_agents ? 1 : 0
  metadata {
    name      = "planner-agent-prompt"
    namespace = kubernetes_namespace.agents.metadata[0].name
  }
  data = {
    "prompt.txt" = <<-EOT
      You are a senior software architect and project planner. Your role is to:
      1. Break down feature requests into well-defined tasks with clear acceptance criteria
      2. Design system architecture and API contracts
      3. Create task dependencies that form an efficient execution DAG
      4. Review completed designs and provide feedback
      5. Escalate blockers and coordinate between team members

      When creating tasks:
      - Each task should be completable by a single agent in under 4 hours
      - Define clear acceptance criteria that can be objectively evaluated
      - Set appropriate priorities (High for blockers, Medium for core work, Low for nice-to-haves)
      - Always include design tasks before implementation tasks
    EOT
  }
}

resource "kubernetes_config_map" "coder_prompt" {
  count = var.enable_example_agents ? 1 : 0
  metadata {
    name      = "coder-agent-prompt"
    namespace = kubernetes_namespace.agents.metadata[0].name
  }
  data = {
    "prompt.txt" = <<-EOT
      You are an expert software engineer. Your role is to:
      1. Implement features based on design specifications
      2. Write clean, well-tested, production-quality code
      3. Follow established patterns and conventions in the codebase
      4. Fix bugs and address review feedback
      5. Write unit tests for all new code

      Guidelines:
      - Always read existing code before making changes
      - Write tests alongside implementation
      - Keep commits atomic and well-described
      - Never commit secrets, credentials, or API keys
      - Follow the project's coding standards
    EOT
  }
}

resource "kubernetes_config_map" "reviewer_prompt" {
  count = var.enable_example_agents ? 1 : 0
  metadata {
    name      = "reviewer-agent-prompt"
    namespace = kubernetes_namespace.agents.metadata[0].name
  }
  data = {
    "prompt.txt" = <<-EOT
      You are a senior code reviewer. Your role is to:
      1. Review code changes for correctness, security, and quality
      2. Check that acceptance criteria are met
      3. Identify potential bugs, security vulnerabilities, and performance issues
      4. Verify test coverage is adequate
      5. Provide constructive, actionable feedback

      Review checklist:
      - Does the code compile and pass tests?
      - Are there any security vulnerabilities (injection, XSS, etc.)?
      - Is error handling appropriate?
      - Are edge cases covered?
      - Is the code readable and maintainable?
      - Does it follow project conventions?
    EOT
  }
}

# KARO CRD manifests applied via kubernetes_manifest
# ModelConfig: Vertex AI Gemini 4
resource "kubernetes_manifest" "vertex_model_config" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "ModelConfig"
    metadata = {
      name      = "gemini4-vertex"
      namespace = kubernetes_namespace.agents.metadata[0].name
    }
    spec = {
      provider = "google-vertex"
      name     = var.vertex_ai_model
      vertex = {
        project            = var.project_id
        location           = var.region
        gcpServiceAccount  = google_service_account.karo_agent.email
      }
      parameters = {
        maxTokens   = 8192
        temperature = 0.3
      }
      rateLimit = {
        requestsPerMinute = 60
        tokensPerMinute   = 1000000
      }
    }
  }
  depends_on = [helm_release.karo]
}

# SandboxClass: secure execution environment
resource "kubernetes_manifest" "agent_sandbox" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "SandboxClass"
    metadata = {
      name      = "gke-sandbox"
      namespace = kubernetes_namespace.agents.metadata[0].name
    }
    spec = {
      runtimeClassName = "gvisor"
      networkPolicy = {
        egress         = "restricted"
        allowedDomains = [
          "api.github.com",
          "github.com",
          "pypi.org",
          "registry.npmjs.org",
          "go.dev",
          "proxy.golang.org",
          "us-central1-aiplatform.googleapis.com",
          "oauth2.googleapis.com",
        ]
        allowedCIDRs = []
      }
      filesystem = {
        readOnlyRootFilesystem = false
        allowedMounts          = ["/tmp", "/workspace"]
      }
      resourceLimits = {
        cpu                 = "2"
        memory              = "4Gi"
        "ephemeral-storage" = "10Gi"
      }
      securityContext = {
        runAsNonRoot             = true
        runAsUser                = 1000
        allowPrivilegeEscalation = false
        seccompProfile = {
          type = "RuntimeDefault"
        }
        capabilities = {
          drop = ["ALL"]
        }
      }
    }
  }
  depends_on = [helm_release.karo]
}

# MemoryStore: shared team memory
resource "kubernetes_manifest" "team_memory" {
  count = var.enable_example_agents && var.mem0_api_key != "" ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "MemoryStore"
    metadata = {
      name      = "gke-team-memory"
      namespace = kubernetes_namespace.agents.metadata[0].name
    }
    spec = {
      backend = {
        type = "mem0"
        mem0 = {
          apiKeySecret = {
            name = "mem0-api-key"
            key  = "API_KEY"
          }
          organizationId = "karo-gke"
          projectId      = "dev-team"
        }
      }
      scope         = "team"
      retentionDays = 90
      maxMemories   = 10000
      categories    = [
        "architecture-decisions",
        "code-patterns",
        "review-feedback",
        "debugging-notes",
      ]
    }
  }
  depends_on = [helm_release.karo]
}

# ToolSet: MCP tools for dev agents
resource "kubernetes_manifest" "dev_tools" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "ToolSet"
    metadata = {
      name      = "dev-tools"
      namespace = kubernetes_namespace.agents.metadata[0].name
    }
    spec = {
      tools = [
        {
          name      = "github"
          type      = "mcp"
          transport = "streamable-http"
          endpoint  = "http://github-mcp-server.${var.agent_namespace}.svc:8080"
          permissions = ["read", "write", "pull-request"]
          credentialSecret = {
            name = "github-token"
            key  = "GITHUB_TOKEN"
          }
        },
        {
          name            = "code-executor"
          type            = "mcp"
          transport       = "stdio"
          command         = ["/usr/local/bin/code-exec-mcp"]
          permissions     = ["execute"]
          sandboxRequired = true
        },
      ]
    }
  }
  depends_on = [helm_release.karo]
}

# AgentPolicy: governance rules
resource "kubernetes_manifest" "dev_policy" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "AgentPolicy"
    metadata = {
      name      = "gke-dev-policy"
      namespace = kubernetes_namespace.agents.metadata[0].name
    }
    spec = {
      targetSelector = {
        matchLabels = {
          team = "gke-dev"
        }
      }
      models = {
        allowedProviders        = ["google-vertex"]
        deniedModels            = []
        requireMinContextWindow = 100000
      }
      toolCalls = {
        maxPerMinute             = 60
        maxPerLoop               = 500
        requireSandboxForExecute = true
      }
      loop = {
        maxIterationsPerRun                = 100
        maxRunDurationMinutes              = 120
        requireHumanApprovalAfterIterations = 50
      }
      audit = {
        enabled  = true
        logLevel = "Full"
        logDestination = {
          type = "stdout"
        }
        retentionDays = 365
      }
      dataClassification = {
        allowedLevels = ["internal", "confidential"]
        denyPatterns  = [".*api[_-]?key.*", ".*password.*", ".*secret.*"]
      }
      escalation = {
        onPolicyViolation = "Block"
      }
    }
  }
  depends_on = [helm_release.karo]
}

# AgentSpec: Planner (orchestrator)
resource "kubernetes_manifest" "planner_agent" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "AgentSpec"
    metadata = {
      name      = "planner-agent"
      namespace = kubernetes_namespace.agents.metadata[0].name
      labels = {
        team = "gke-dev"
        role = "orchestrator"
      }
    }
    spec = {
      modelConfigRef = {
        name = "gemini4-vertex"
      }
      systemPrompt = {
        configMapRef = {
          name = "planner-agent-prompt"
          key  = "prompt.txt"
        }
      }
      capabilities = [
        {
          name = "design"
          skillPrompt = {
            inline = "Create detailed design documents with architecture diagrams, API contracts, data models, and sequence diagrams for key flows."
          }
        },
        {
          name = "review"
          skillPrompt = {
            inline = "Review submitted work against the task's acceptance criteria. Provide specific, actionable feedback."
          }
        },
      ]
      runtime = {
        image = var.claude_code_harness_image
        resources = {
          requests = { cpu = "500m", memory = "1Gi" }
          limits   = { cpu = "2", memory = "4Gi" }
        }
      }
      maxContextTokens   = 200000
      onContextExhaustion = "checkpoint"
      dispatchable       = true
      scaling = {
        minInstances    = 0
        maxInstances    = 2
        startPolicy     = "OnDemand"
        cooldownSeconds = 300
      }
    }
  }
  depends_on = [helm_release.karo, kubernetes_manifest.vertex_model_config]
}

# AgentSpec: Coder (executor)
resource "kubernetes_manifest" "coder_agent" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "AgentSpec"
    metadata = {
      name      = "coder-agent"
      namespace = kubernetes_namespace.agents.metadata[0].name
      labels = {
        team = "gke-dev"
        role = "executor"
      }
    }
    spec = {
      modelConfigRef = {
        name = "gemini4-vertex"
      }
      systemPrompt = {
        configMapRef = {
          name = "coder-agent-prompt"
          key  = "prompt.txt"
        }
      }
      capabilities = [
        {
          name = "impl"
          skillPrompt = {
            inline = "Implement the feature as specified. Write clean code with unit tests. Commit to the feature branch."
          }
          requiredTools = ["github", "code-executor"]
        },
        {
          name          = "debugging"
          requiredTools = ["code-executor"]
        },
      ]
      sandboxClassRef = {
        name = "gke-sandbox"
      }
      toolSetRef = {
        name = "dev-tools"
      }
      workspaceCredentials = {
        git = [
          {
            name = "github"
            type = "token"
            host = "github.com"
            credentialSecret = {
              name = "github-token"
              key  = "GITHUB_TOKEN"
            }
            scope = "push"
          },
        ]
      }
      runtime = {
        image = var.claude_code_harness_image
        resources = {
          requests = { cpu = "1", memory = "2Gi" }
          limits   = { cpu = "4", memory = "8Gi" }
        }
      }
      maxContextTokens   = 200000
      onContextExhaustion = "restart"
      dispatchable       = true
      scaling = {
        minInstances    = 0
        maxInstances    = 5
        startPolicy     = "OnDemand"
        cooldownSeconds = 300
      }
    }
  }
  depends_on = [helm_release.karo, kubernetes_manifest.vertex_model_config]
}

# AgentSpec: Reviewer (evaluator)
resource "kubernetes_manifest" "reviewer_agent" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "AgentSpec"
    metadata = {
      name      = "reviewer-agent"
      namespace = kubernetes_namespace.agents.metadata[0].name
      labels = {
        team = "gke-dev"
        role = "evaluator"
      }
    }
    spec = {
      modelConfigRef = {
        name = "gemini4-vertex"
      }
      systemPrompt = {
        configMapRef = {
          name = "reviewer-agent-prompt"
          key  = "prompt.txt"
        }
      }
      capabilities = [
        {
          name = "review"
          skillPrompt = {
            inline = "Review submitted code changes: check correctness, security, performance. Verify test coverage. Provide line-level feedback."
          }
          requiredTools = ["github"]
        },
      ]
      toolSetRef = {
        name = "dev-tools"
      }
      runtime = {
        image = var.claude_code_harness_image
        resources = {
          requests = { cpu = "500m", memory = "1Gi" }
          limits   = { cpu = "2", memory = "4Gi" }
        }
      }
      maxContextTokens   = 200000
      onContextExhaustion = "checkpoint"
      dispatchable       = true
      scaling = {
        minInstances    = 0
        maxInstances    = 3
        startPolicy     = "OnDemand"
        cooldownSeconds = 300
      }
    }
  }
  depends_on = [helm_release.karo, kubernetes_manifest.vertex_model_config]
}

# Dispatcher: capability-based routing
resource "kubernetes_manifest" "dev_dispatcher" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "Dispatcher"
    metadata = {
      name      = "gke-dev-router"
      namespace = kubernetes_namespace.agents.metadata[0].name
    }
    spec = {
      mode = "capability"
      taskGraphSelector = {
        matchLabels = {
          team = "gke-dev"
        }
      }
      capabilityRoutes = [
        { capability = "design",  agentSpecRef = { name = "planner-agent" } },
        { capability = "impl",    agentSpecRef = { name = "coder-agent" } },
        { capability = "review",  agentSpecRef = { name = "reviewer-agent" } },
      ]
      fallbackAgentSpecRef = {
        name = "planner-agent"
      }
      messaging = {
        type           = "mailbox"
        mailboxPattern = "{agentSpec}-mailbox"
      }
    }
  }
  depends_on = [helm_release.karo]
}

# AgentTeam: bind all agents together
resource "kubernetes_manifest" "dev_team" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "AgentTeam"
    metadata = {
      name      = "gke-dev-team"
      namespace = kubernetes_namespace.agents.metadata[0].name
    }
    spec = {
      description = "GKE development team with Vertex AI Gemini 4 and Claude Code harness"
      agents = [
        { agentSpecRef = { name = "planner-agent" },  role = "orchestrator" },
        { agentSpecRef = { name = "coder-agent" },     role = "executor" },
        { agentSpecRef = { name = "reviewer-agent" },  role = "evaluator" },
      ]
      sharedResources = {
        toolSetRef      = { name = "dev-tools" }
        sandboxClassRef = { name = "gke-sandbox" }
        modelConfigRef  = { name = "gemini4-vertex" }
      }
      dispatcherRef = { name = "gke-dev-router" }
      policyRef     = { name = "gke-dev-policy" }
    }
  }
  depends_on = [
    kubernetes_manifest.planner_agent,
    kubernetes_manifest.coder_agent,
    kubernetes_manifest.reviewer_agent,
    kubernetes_manifest.dev_dispatcher,
    kubernetes_manifest.dev_policy,
  ]
}

# TaskGraph: starter task to verify the team works
resource "kubernetes_manifest" "starter_taskgraph" {
  count = var.enable_example_agents ? 1 : 0
  manifest = {
    apiVersion = "karo.dev/v1alpha1"
    kind       = "TaskGraph"
    metadata = {
      name      = "quickstart-hello"
      namespace = kubernetes_namespace.agents.metadata[0].name
      labels = {
        team = "gke-dev"
      }
    }
    spec = {
      description  = "Quickstart verification: design and implement a hello-world REST endpoint"
      ownerAgentRef = { name = "planner-agent" }
      dispatcherRef = { name = "gke-dev-router" }
      tasks = [
        {
          id          = "design-hello"
          title       = "Design hello-world API endpoint"
          type        = "design"
          description = "Define a simple GET /hello endpoint that returns {\"message\": \"Hello from KARO on GKE!\"}. Create an OpenAPI snippet."
          deps        = []
          priority    = "High"
          addedBy     = "planner-agent"
          addedAt     = "2026-04-17T00:00:00Z"
          timeoutMinutes = 30
          acceptanceCriteria = [
            "OpenAPI spec defined for GET /hello",
            "Response schema documented",
          ]
        },
        {
          id          = "impl-hello"
          title       = "Implement hello-world endpoint"
          type        = "impl"
          description = "Implement the GET /hello endpoint as designed. Include a unit test."
          deps        = ["design-hello"]
          priority    = "High"
          addedBy     = "planner-agent"
          addedAt     = "2026-04-17T00:00:00Z"
          timeoutMinutes = 60
          acceptanceCriteria = [
            "Endpoint returns 200 with correct JSON",
            "Unit test passes",
          ]
        },
        {
          id          = "review-hello"
          title       = "Review hello-world implementation"
          type        = "review"
          description = "Review the implementation for correctness and completeness."
          deps        = ["impl-hello"]
          priority    = "Medium"
          addedBy     = "planner-agent"
          addedAt     = "2026-04-17T00:00:00Z"
          timeoutMinutes = 30
          acceptanceCriteria = [
            "Code review completed with no blocking issues",
          ]
        },
      ]
      dispatchPolicy = {
        maxConcurrent        = 2
        defaultTimeoutMinutes = 60
        retryPolicy = {
          maxRetries     = 1
          backoffSeconds = 30
          onExhaustion   = "EscalateToHuman"
        }
        allowAgentMutation = false
      }
    }
  }
  depends_on = [kubernetes_manifest.dev_team]
}
