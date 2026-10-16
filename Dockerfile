FROM debian:trixie
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get -q update && \
    apt-get -y upgrade && \
    apt-get -y install --no-install-recommends apache2 ssl-cert && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*
RUN a2enmod rewrite
COPY /start.sh /
RUN chmod a+rx /start.sh
EXPOSE 80
EXPOSE 443
VOLUME /etc/ssl
ENTRYPOINT ["/start.sh"]
