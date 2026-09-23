select repo, stars, forks, stars + forks as engagement
from {{ source('ingestion', 'github_repo_stats') }}
