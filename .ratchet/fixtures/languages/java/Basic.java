class Basic {
    // it's a comment
    String s = """
        a "quoted" // not a comment
        """;
    char c = '"';
    @Test
    public void adds() {}
    @SuppressWarnings("unchecked")
    void f() {} // NOPMD
    /* block 'quote' */
}
