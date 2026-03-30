FROM ghcr.io/block/goose:latest

COPY bootstrap.sh /usr/local/bin/karo-bootstrap
COPY generate-goose-config.sh /usr/local/bin/karo-generate-goose-config
COPY build-task-prompt.sh /usr/local/bin/karo-build-task-prompt
COPY recipes/ /etc/karo/recipes/

RUN chmod +x /usr/local/bin/karo-bootstrap \
    /usr/local/bin/karo-generate-goose-config \
    /usr/local/bin/karo-build-task-prompt

ENTRYPOINT ["/usr/local/bin/karo-bootstrap"]
