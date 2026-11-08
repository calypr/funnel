# Overview 🔐

Thanks to our collaborators at CTDS — Funnel is currently adding support for "Per-User/Per-Bucket" credentials to allow Users to access S3 Buckets without having to store their credentials in the Funnel Server.

The high level overview of this feature will be such Funnel will be able to speak with a custom credential "Wrapper Script" that will:

- Take the User Credentials
- Create an S3 Bucket
- Generate a Key (optionally for use in Nextflow Config)
- Send the Key to Funnel

In this way this Wrapper can manage the bucket and the keys (the Wrapper would be the middleware between the User and Funnel).

Stay tuned for this feature's development! This feature is being tracked with the following:

- GitHub Branch: https://github.com/ohsu-comp-bio/funnel/tree/feature/credentials
- Pull Request: https://github.com/ohsu-comp-bio/funnel/pull/1098

# Credits 🙌

This feature and its development would not be possible without our continuing collaboration with [Pauline Ribeyre](https://github.com/paulineribeyre), [Jawad Qureshi](https://github.com/jawadqur), [Michael Fitzsimons](https://www.linkedin.com/in/michael-fitzsimons-ab8a6111), and the entire [CTDS](https://ctds.uchicago.edu) team at the [University of Chicago](https://www.uchicago.edu/)!
