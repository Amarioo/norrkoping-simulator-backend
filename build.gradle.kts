plugins {
    kotlin("jvm") version "2.4.21"
    kotlin("plugin.serialization") version "2.4.21"
    application
}
repositories { mavenCentral() }
group = "com.norrkoping.simulator"
version = "0.1.0"
kotlin { compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_21) } }
java { sourceCompatibility = JavaVersion.VERSION_21; targetCompatibility = JavaVersion.VERSION_21 }
application { mainClass.set("com.norrkoping.simulator.ApplicationKt") }
dependencies {
    implementation(platform("io.ktor:ktor-bom:3.6.0"))
    implementation("io.ktor:ktor-server-core-jvm")
    implementation("io.ktor:ktor-server-netty-jvm")
    implementation("io.ktor:ktor-server-sse-jvm")
    implementation("io.ktor:ktor-server-content-negotiation-jvm")
    implementation("io.ktor:ktor-serialization-kotlinx-json-jvm")
    implementation("io.ktor:ktor-server-status-pages-jvm")
    implementation("ch.qos.logback:logback-classic:1.5.32")
    testImplementation(kotlin("test-junit5"))
    testImplementation("io.ktor:ktor-server-test-host-jvm")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}
tasks.test { useJUnitPlatform() }