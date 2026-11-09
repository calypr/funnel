# Modified from: https://github.com/giovtorres/docker-centos7-slurm/blob/master/17.02.3/Dockerfile
FROM centos:7

RUN yum makecache fast \
    && yum -y install epel-release \
    && yum -y install \
        wget \
        bzip2 \
        perl \
        gcc \
        gcc-c++\
        git \
        make \
        munge \
        munge-devel \
        supervisor \
        python-devel \
        python-pip \
        mariadb-server \
        mariadb-devel \
        psmisc \
        bash-completion \
        docker \
    && yum clean all

RUN pip install Cython nose nose2
RUN groupadd -r slurm && useradd -r -g slurm slurm

ENV SLURM_VERSION 17.02.9
ENV SLURM_DOWNLOAD_MD5 6bd0b38e6bf08f3426a7dd1e663a2e3c
ENV SLURM_DOWNLOAD_URL https://download.schedmd.com/slurm/slurm-"$SLURM_VERSION".tar.bz2

RUN set -x \
    && wget -O slurm.tar.bz2 "$SLURM_DOWNLOAD_URL" \
    && echo "$SLURM_DOWNLOAD_MD5" slurm.tar.bz2 | md5sum -c - \
    && mkdir /usr/local/src/slurm \
    && tar jxf slurm.tar.bz2 -C /usr/local/src/slurm --strip-components=1 \
    && rm slurm.tar.bz2 \
    && cd /usr/local/src/slurm \
    && ./configure --enable-debug --enable-front-end --prefix=/usr \
       --sysconfdir=/etc/slurm --with-mysql_config=/usr/bin \
       --libdir=/usr/lib64 \
    && make install \
    && install -D -m644 etc/cgroup.conf.example /etc/slurm/cgroup.conf.example \
    && install -D -m644 etc/slurm.conf.example /etc/slurm/slurm.conf.example \
    && install -D -m644 etc/slurm.epilog.clean /etc/slurm/slurm.epilog.clean \
    && install -D -m644 etc/slurmdbd.conf.example /etc/slurm/slurmdbd.conf.example \
    && install -D -m644 contribs/slurm_completion_help/slurm_completion.sh /etc/profile.d/slurm_completion.sh \
    && cd \
    && rm -rf /usr/local/src/slurm \
    && mkdir /etc/sysconfig/slurm \
        /var/spool/slurmd \
        /var/run/slurmd \
        /var/lib/slurmd \
        /var/log/slurm \
    && /sbin/create-munge-key

COPY slurm.conf /etc/slurm/slurm.conf
COPY slurmdbd.conf /etc/slurm/slurmdbd.conf
COPY supervisord.conf /etc/
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

VOLUME ["/var/lib/mysql", "/var/lib/slurmd", "/var/spool/slurmd", "/var/log/slurm"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/bin/bash"]
