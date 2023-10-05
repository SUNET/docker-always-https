FROM debian:stable
MAINTAINER leifj@sunet.se
RUN echo 'debconf debconf/frontend select Noninteractive' | debconf-set-selections
RUN apt-get -q update
RUN apt-get -y upgrade
RUN apt-get -y install apache2
RUN a2enmod rewrite
COPY /start.sh /
RUN chmod a+rx /start.sh
EXPOSE 80
EXPOSE 443
VOLUME /etc/ssl
ENTRYPOINT ["/start.sh"]
