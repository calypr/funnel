FROM centos:7

RUN yum -y install epel-release && \
    yum -y --enablerepo=centosplus install \
        make perl-CPAN openssl-devel libxml2-devel \
        boost-devel gcc gcc-c++ git tar libtool vim-minimal \
        supervisor docker

WORKDIR /usr/local

# Pull torque
RUN git clone https://github.com/adaptivecomputing/torque.git -b 5.0.0 5.0.0

WORKDIR /usr/local/5.0.0
RUN ./autogen.sh

# Build Torque
RUN ./configure
RUN make
RUN make install
RUN cp contrib/init.d/trqauthd /etc/init.d/
RUN cp contrib/init.d/pbs_mom /etc/init.d/pbs_mom
RUN cp contrib/init.d/pbs_server /etc/init.d/pbs_server
RUN cp contrib/init.d/pbs_sched /etc/init.d/pbs_sched
RUN ldconfig

# Configure Torque
RUN echo "localhost" > /var/spool/torque/server_name
RUN echo '/usr/local/lib' > /etc/ld.so.conf.d/torque.conf
RUN ldconfig
ENV HOSTNAME localhost

RUN cat /etc/hosts
ADD torque.setup /usr/local/5.0.0/torque.setup
RUN trqauthd start && \
    ./torque.setup root localhost && \
    pbs_mom && \
    pbs_sched && \
    qmgr -c "set server scheduling=True"

RUN echo "localhost np=1" >> /var/spool/torque/server_priv/nodes
RUN echo "docker np=1" >> /var/spool/torque/server_priv/nodes
RUN printf "\$pbsserver localhost" >> /var/spool/torque/mom_priv/config

# create a new user since you can't submit jobs as root
RUN yum install -y sudo
RUN adduser testuser && \
    echo "testuser:testuser" | chpasswd
RUN echo "testuser ALL=NOPASSWD:ALL" >> /etc/sudoers 
RUN usermod -aG input testuser

ADD supervisord.conf /etc/supervisord.conf
RUN chmod -R 777 /var/log/supervisor

ADD docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

USER testuser
ADD torque.submit /home/testuser/torque.submit
WORKDIR /home/testuser
VOLUME /home/testuser

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/bin/bash"]
