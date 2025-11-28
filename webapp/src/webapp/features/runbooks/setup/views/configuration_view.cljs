(ns webapp.features.runbooks.setup.views.configuration-view
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Link
                               RadioGroup Text]]
   ["lucide-react" :refer [Plus Trash2]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.config :as config]))

(defn- get-repository-type
  "Detect if repository is public or private, and credential type"
  [repo]
  (let [has-ssh-key? (seq (:ssh_key repo))
        has-http-credentials? (or (seq (:git_password repo))
                                  (seq (:git_user repo)))
        repo-type (if (or has-ssh-key? has-http-credentials?)
                    "private"
                    "public")
        cred-type (cond
                    has-ssh-key? "ssh"
                    has-http-credentials? "http"
                    :else "http")]
    {:repository-type repo-type
     :credential-type cred-type}))

(defn- api-repo->ui-repo
  "Convert API repository format (snake_case) to UI format"
  [api-repo]
  (let [detected (get-repository-type api-repo)]
    {:git-url (:git_url api-repo "")
     :repository-type (:repository-type detected)
     :credential-type (:credential-type detected)
     :http-user (:git_user api-repo "")
     :http-token (:git_password api-repo "")
     :ssh-key (:ssh_key api-repo "")
     :ssh-user (:ssh_user api-repo "")
     :ssh-key-password (:ssh_keypass api-repo "")
     :ssh-known-hosts (:ssh_known_hosts api-repo "")
     :git-hook-ttl (:git_hook_ttl api-repo)}))

(defn- ui-repo->api-repo
  "Convert UI repository format to API format (snake_case)"
  [ui-repo]
  (let [base {:git_url (:git-url ui-repo)}]
    (cond
      ;; Public repository - only git_url
      (= (:repository-type ui-repo) "public")
      base

      ;; Private HTTP repository
      (and (= (:repository-type ui-repo) "private")
           (= (:credential-type ui-repo) "http"))
      (cond-> base
        (seq (:http-user ui-repo))
        (assoc :git_user (:http-user ui-repo))
        (seq (:http-token ui-repo))
        (assoc :git_password (:http-token ui-repo)))

      ;; Private SSH repository
      (and (= (:repository-type ui-repo) "private")
           (= (:credential-type ui-repo) "ssh"))
      (cond-> base
        (seq (:ssh-key ui-repo))
        (assoc :ssh_key (:ssh-key ui-repo))
        (seq (:ssh-user ui-repo))
        (assoc :ssh_user (:ssh-user ui-repo))
        (seq (:ssh-key-password ui-repo))
        (assoc :ssh_keypass (:ssh-key-password ui-repo))
        (seq (:ssh-known-hosts ui-repo))
        (assoc :ssh_known_hosts (:ssh-known-hosts ui-repo))
        (:git-hook-ttl ui-repo)
        (assoc :git_hook_ttl (:git-hook-ttl ui-repo)))

      :else base)))

(defn- repository-form
  "Render a single repository configuration form"
  [repo-index repositories-atom on-remove]
  (let [repo (get @repositories-atom repo-index)
        update-field (fn [field value]
                       (swap! repositories-atom assoc-in [repo-index field] value))
        repository-type (get repo :repository-type "public")
        credential-type (get repo :credential-type "http")
        git-url (get repo :git-url "")
        http-user (get repo :http-user "")
        http-token (get repo :http-token "")
        ssh-key (get repo :ssh-key "")
        ssh-user (get repo :ssh-user "")
        ssh-key-password (get repo :ssh-key-password "")
        ssh-known-hosts (get repo :ssh-known-hosts "")]
    [:> Box {:class "space-y-radix-7"}
     ;; Repository Privacy Type Section
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Repository privacy type"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Select the type of Git repository to connect."]]

      [:> Box {:grid-column "span 5 / span 5"}
       [:> RadioGroup.Root {:on-value-change #(update-field :repository-type %)
                            :value repository-type}
        [:> RadioGroup.Item {:value "public"}
         [:> Text {:size "3"} "Public"]]
        [:> RadioGroup.Item {:value "private"}
         [:> Text {:size "3"} "Private"]]]]]

     ;; Repository Credentials Section
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Repository credentials"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Provide repository access information."]]

      [:> Box {:grid-column "span 5 / span 5" :class "space-y-radix-4"}
       ;; Credential type selection (only for private repos)
       (when (= repository-type "private")
         [:> RadioGroup.Root {:onValueChange #(update-field :credential-type %)
                              :value credential-type}
          [:> RadioGroup.Item {:value "http"}
           [:> Text {:size "3"} "HTTP"]]
          [:> RadioGroup.Item {:value "ssh"}
           [:> Text {:size "3"} "SSH"]]])

       ;; Git URL (always visible)
       [forms/input {:label "Git URL"
                     :placeholder "github.com/hoophq/runbooks"
                     :value git-url
                     :on-change #(update-field :git-url (-> % .-target .-value))
                     :class "w-full"}]

       ;; HTTP credentials (for private HTTP repos)
       (when (and (= repository-type "private")
                  (= credential-type "http"))
         [:<>
          [forms/input {:label "User (Optional)"
                        :placeholder "············"
                        :type "password"
                        :value http-user
                        :on-change #(update-field :http-user (-> % .-target .-value))
                        :class "w-full"}]
          [:> Text {:size "2" :class "text-[--gray-11] -mt-2 mb-4"}
           "Defaults to 'oauth2'. Override only for custom Git providers that require specific usernames."]

          [forms/input {:label "Access Password or Token"
                        :type "password"
                        :placeholder "············"
                        :value http-token
                        :on-change #(update-field :http-token (-> % .-target .-value))
                        :class "w-full"}]])

       ;; SSH credentials (for private SSH repos)
       (when (and (= repository-type "private")
                  (= credential-type "ssh"))
         [:<>
          [forms/textarea {:label "SSH Key"
                           :placeholder "············"
                           :value ssh-key
                           :on-change #(update-field :ssh-key (-> % .-target .-value))
                           :class "w-full"
                           :rows 6}]

          [forms/input {:label "SSH User (Optional)"
                        :placeholder "············"
                        :type "password"
                        :value ssh-user
                        :on-change #(update-field :ssh-user (-> % .-target .-value))
                        :class "w-full"}]

          [forms/input {:label "SSH Key Password"
                        :type "password"
                        :placeholder "············"
                        :value ssh-key-password
                        :on-change #(update-field :ssh-key-password (-> % .-target .-value))
                        :class "w-full"}]

          [forms/textarea {:label "SSH Known Hosts File (Optional)"
                           :placeholder "hostname[,ip] key-type public-key [comment]"
                           :value ssh-known-hosts
                           :on-change #(update-field :ssh-known-hosts (-> % .-target .-value))
                           :class "w-full"
                           :rows 4}]])]]

     (when on-remove
       [:> Flex {:justify "end"}
        [:> Button {:size "2"
                    :variant "ghost"
                    :color "red"
                    :on-click on-remove}
         [:> Trash2 {:size 14}]
         "Remove"]])]))

(defn main [_active-tab]
  (let [config-data (rf/subscribe [:runbooks-configurations/data])
        repositories-atom (r/atom [])
        config-loaded (r/atom false)
        is-submitting (r/atom false)

        add-repository (fn []
                         (swap! repositories-atom conj
                                {:git-url ""
                                 :repository-type "public"
                                 :credential-type "http"
                                 :http-user ""
                                 :http-token ""
                                 :ssh-key ""
                                 :ssh-user ""
                                 :ssh-key-password ""
                                 :ssh-known-hosts ""}))

        remove-repository (fn [index]
                            (swap! repositories-atom
                                   (fn [repos]
                                     (vec (keep-indexed (fn [i repo]
                                                          (when (not= i index) repo))
                                                        repos)))))

        handle-save (fn []
                      (let [repositories @repositories-atom
                            empty-repos (filter #(empty? (str/trim (:git-url %))) repositories)]
                        (if (seq empty-repos)
                          (do
                            (rf/dispatch [:show-snackbar
                                          {:level :error
                                           :text "Please fill in the Git URL for all repositories before saving."}])
                            (reset! is-submitting false))
                          (do
                            (reset! is-submitting true)
                            (let [api-repositories (mapv ui-repo->api-repo repositories)
                                  on-success (fn []
                                               (rf/dispatch [:show-snackbar
                                                             {:level :success
                                                              :text "Git repositories configured!"}])
                                               (reset! config-loaded false)
                                               (js/setTimeout
                                                (fn []
                                                  (rf/dispatch [:runbooks-configurations/get])
                                                  (js/setTimeout
                                                   (fn []
                                                     (reset! is-submitting false))
                                                   200))
                                                200))
                                  on-failure (fn []
                                               (reset! is-submitting false))]
                              (rf/dispatch [:runbooks-configurations/update api-repositories on-success on-failure]))))))] 

    (fn []
      (let [config-status (:status @config-data)
            config-response (:data @config-data)]

        ;; Load repositories from API response
        (when (and (not @config-loaded)
                   (or (= config-status :success)
                       (= config-status :error)))
          (reset! config-loaded true)
          (if (and (= config-status :success) config-response)
            (let [api-repositories (:repositories config-response [])]
              (if (empty? api-repositories)
                ;; If no repositories, add one default empty repository
                (reset! repositories-atom
                        [{:git-url ""
                          :repository-type "public"
                          :credential-type "http"
                          :http-user ""
                          :http-token ""
                          :ssh-key ""
                          :ssh-user ""
                          :ssh-key-password ""
                          :ssh-known-hosts ""}])
                ;; Convert API repositories to UI format
                (reset! repositories-atom
                        (mapv api-repo->ui-repo api-repositories))))

            (when (empty? @repositories-atom)
              (reset! repositories-atom
                      [{:git-url ""
                        :repository-type "public"
                        :credential-type "http"
                        :http-user ""
                        :http-token ""
                        :ssh-key ""
                        :ssh-user ""
                        :ssh-key-password ""
                        :ssh-known-hosts ""}]))))

        [:> Box {:pb "7" :class "space-y-radix-7"}
         [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-[--gray-12]"}
          "Git Repositories"]
         [:> Text {:size "3" :class "text-[--gray-11] mt-2"}
          "Add your repositories to start automating Runbooks. Learn more in our "
          [:> Link {:href (get-in config/docs-url [:features :runbooks])
                    :target "_blank"}
           "Runbooks documentation."]]

         ;; Repository Blocks
         (when (seq @repositories-atom)
           (map-indexed
            (fn [index _]
              ^{:key index}
              [:> Box {:class (when (> index 0) "mt-7")}
               [repository-form index repositories-atom
                (when (> index 0)
                  #(remove-repository index))]])
            @repositories-atom))

         [:> Grid {:columns "7" :gap "7" :mt "7"}
          [:> Box {:grid-column "span 2 / span 2"}]
          [:> Box {:grid-column "span 5 / span 5"}
           [:> Flex {:direction "column" :gap "4" :align "start"}
            [:> Button {:size "3"
                        :variant "soft"
                        :on-click add-repository}
             [:> Plus {:size 16}]
             " Add Repository"]
            [:> Button {:size "4"
                        :loading @is-submitting
                        :disabled @is-submitting
                        :on-click handle-save}
             "Save"]]]]]))))
