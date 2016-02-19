FROM ubuntu
MAINTAINER leifj@sunet.se
RUN echo 'debconf debconf/frontend select Noninteractive' | debconf-set-selections
RUN apt-get update
RUN apt-get -y install apache2
RUN a2enmod rewrite
RUN mkdir -p /var/www
COPY _lvs.txt /var/www/
COPY /start.sh /
RUN chmod a+rx /start.sh
COPY /apache2.conf /etc/apache2/
EXPOSE 80
EXPOSE 443
VOLUME /etc/ssl
ENTRYPOINT ["/start.sh"]
