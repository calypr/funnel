FROM centos:7

MAINTAINER Adam Struck <strucka@ohsu.edu>

# Add condor user
RUN adduser condor && \
    echo "condor:condor" | chpasswd

# Build in one RUN
RUN yum -y install \
         docker \
         epel-release \
         openssh-clients \
         openssh-server \
         sudo \
         tar \ 
         which \
    && \
    curl -O http://research.cs.wisc.edu/htcondor/yum/RPM-GPG-KEY-HTCondor && \
    rpm --import RPM-GPG-KEY-HTCondor && \
    yum-config-manager --add-repo https://research.cs.wisc.edu/htcondor/yum/repo.d/htcondor-stable-rhel7.repo && \
    yum -y install --enablerepo=centosplus condor && \
    yum clean all && \
    rm -f RPM-GPG-KEY-HTCondor

# add condor config
ADD ./condor.config /etc/condor/condor_config.local
ADD ./slots.config /etc/condor/config.d/00-slots
VOLUME /var/lib/condor
VOLUME /etc/condor

ADD docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

WORKDIR /home/condor

RUN echo "condor ALL=NOPASSWD:ALL" >> /etc/sudoers 
RUN usermod -aG input condor
USER condor

ADD test_condor.submit ./

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/bin/bash"]
