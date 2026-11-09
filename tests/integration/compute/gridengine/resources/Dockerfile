# Modified from: https://bitbucket.org/deardooley/agave-docker/src//test-containers/schedulers/gridengine/?at=master
FROM centos:6

# Install GridEngine
RUN yum -y install epel-release && \
    yum install -y \
        gridengine \
        gridengine-qmaster \
        gridengine-execd \
        gridengine-qmon \
        gridengine-devel \
        docker-io \
    && yum clean all

# Configure GridEngine
WORKDIR /usr/share/gridengine/
ADD docker_configuration.conf /usr/share/gridengine/docker_configuration.conf
ADD hostgroup.conf /usr/share/gridengine/hostgroup.conf
ADD debug.queue /usr/share/gridengine/debug.queue
ADD pe.template /usr/share/gridengine/pe.template

## Patch the os check which does not support this version of linux
RUN sed -i 's/osrelease="`$UNAME -r`"/osrelease="2.6.1"/g' util/arch

# Install the master and execution servers
RUN echo "$(grep "$HOSTNAME" /etc/hosts | awk '{print $1;}') docker" >> /etc/hosts && \
    echo "domain docker" >> /etc/resolv.conf && \
    ./inst_sge -x -m -auto /usr/share/gridengine/docker_configuration.conf && \
    cd /usr/share/gridengine/default/common && \
    source /usr/share/gridengine/default/common/settings.sh && \
    echo docker > act_qmaster && \
    qconf -Mhgrp /usr/share/gridengine/hostgroup.conf && \
    qconf -Ap /usr/share/gridengine/pe.template && \
    qconf -Aq /usr/share/gridengine/debug.queue && \
    qconf -Mq /usr/share/gridengine/debug.queue

# Add in a test submit script
ADD gridengine.submit /opt/gridengine.submit

## Add entrypoint script to set the current hostname so the scheduler can communicate
ADD docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

WORKDIR /opt/
VOLUME /opt/

ENTRYPOINT [ "/usr/local/bin/docker-entrypoint.sh" ]
CMD ["/bin/bash"]
