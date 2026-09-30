class Basic {
    // it's a comment
    string v = @"a ""quoted"" \";
    string r = """raw "text" here""";
    char c = '"';
    [Fact]
    public void Adds() {}
#pragma warning disable CS0168
    [ExcludeFromCodeCoverage]
    void F() {}
}
