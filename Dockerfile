FROM scratch
COPY multiverse /multiverse
EXPOSE 8080
ENTRYPOINT ["/multiverse"]
