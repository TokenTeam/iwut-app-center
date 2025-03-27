namespace TokenAppCenter.Model
{
    public class AppCenterConfigResp
    {
        public IList<AppConfig> App { get; set; } = [];
    }

    public class AppConfig
    {
        public string Id { get; set; } = null!;
        public string Name { get; set; } = null!;
        public string Url { get; set; } = null!;
        public string Icon { get; set; } = null!;
        public bool Show { get; set; }
        public string Version { get; set; } = null!;
    }
}
