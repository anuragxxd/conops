export const siteConfig = {
  name: "ConOps",
  description: "GitOps for Docker Compose. Like Argo CD, but without Kubernetes.",
  links: {
    github: "https://github.com/anuragxxd/conops",
    twitter: "https://twitter.com/anurag_1201",
  },
  hero: {
    title: "GitOps for Docker Compose",
    subtitle: "Argo CD style reconciliation. No Kubernetes required.",
    installCommand: `docker run \\
  --name conops \\
  -p 8080:8080 \\
  -e CONOPS_RUNTIME_DIR=/tmp/conops-runtime \\
  -v /tmp/conops-runtime:/tmp/conops-runtime \\
  -v conops_data:/data \\
  -v /var/run/docker.sock:/var/run/docker.sock \\
  anurag1201/conops:latest`
  }
}
